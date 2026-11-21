"""Offline tests for BOQ reconciliation and its benchmark scoring."""

import unicodedata
import unittest

from validation.evaluate_matching import _summarize, evaluate
from validation.matching import BoqMatcher, levenshtein_distance, normalize_unit


def boq(code, description, unit):
    return {"itemCode": code, "description": description, "unitOfMeasure": unit}


def quoted(description, unit):
    return {"rawDescription": description, "unitOfMeasure": unit}


class MatchingTests(unittest.TestCase):
    def test_levenshtein_distance(self):
        self.assertEqual(levenshtein_distance("kitten", "sitting"), 3)
        self.assertEqual(levenshtein_distance("sitting", "kitten"), 3)
        self.assertEqual(levenshtein_distance("", "örnek"), 5)
        self.assertEqual(levenshtein_distance("İstanbul", "İstanbul"), 0)

    def test_normalizes_turkish_case_accents_and_word_order(self):
        text = unicodedata.normalize("NFD", "İÇ-CEPHE ÇELİK KİRİŞ BOYASI")
        matcher = BoqMatcher([boq("P1", text, "m²"), boq("P2", "Dış cephe çelik kiriş boyası", "m2")])
        result = matcher.match_item(quoted("boyasi / kiris celik  ic cephe", "METREKARE"))

        self.assertEqual(result.status, "matched")
        self.assertEqual(result.item_code, "P1")
        self.assertEqual([candidate.item_code for candidate in result.candidates], ["P1"])
        self.assertEqual(result.candidates[0].score, 1)

    def test_edit_distance_matches_a_small_spelling_error(self):
        matcher = BoqMatcher([boq("A", "Çelik ankraj plakası", "pcs")])
        result = matcher.match_item(quoted("Çelik ankaraj plakası", "adet"))

        self.assertEqual(result.item_code, "A")
        self.assertLess(result.candidates[0].score, 1)

    def test_unit_aliases_and_cross_unit_rejection(self):
        for alias, canonical in [("m³", "m3"), ("M^2", "m2"), ("metreküp", "m3"), ("adet", "pcs"),
                                 ("SAAT", "h"), ("sefer", "trip"), ("weeks", "week"), ("tonne", "ton")]:
            with self.subTest(alias=alias):
                self.assertEqual(normalize_unit(alias), canonical)

        matcher = BoqMatcher([boq("MASS", "Nervürlü donatı çeliği", "kg")])
        result = matcher.match_item(quoted("Nervürlü donatı çeliği", "ton"))
        self.assertEqual(result.status, "unmatched")
        self.assertEqual(result.candidates, ())

    def test_diameter_aliases_exclude_conflicting_values(self):
        matcher = BoqMatcher([boq("D12", "Ø12, nervürlü çelik çubuk", "kg"),
                              boq("D16", "Ø16 nervürlü çelik çubuk", "kg")])
        result = matcher.match_item(quoted("1,2 cm çap nervürlü çelik çubuk", "kilogram"))

        self.assertEqual(result.item_code, "D12")
        self.assertEqual([candidate.item_code for candidate in result.candidates], ["D12"])

    def test_nominal_and_measured_diameter_need_different_evidence(self):
        description = "galvanizli basınçlı yangın tesisatı borusu"
        matcher = BoqMatcher([boq("DN80", f"DN-80 {description}", "m")])
        self.assertEqual(matcher.match_item(quoted(f"{description} DN 80mm", "metre")).item_code, "DN80")
        self.assertEqual(matcher.match_item(quoted(f"DN100 {description}", "m")).status, "unmatched")

        result = matcher.match_item(quoted(f"Ø80 {description}", "m"))
        self.assertEqual(result.status, "ambiguous")
        self.assertIsNone(result.item_code)
        self.assertEqual(result.candidates[0].unconfirmed_specs, ("diameter", "nominal diameter"))

    def test_measured_and_compound_dimensions_use_consistent_units(self):
        insulation = BoqMatcher([boq("75", "Yalıtım levhası 75 mm", "m2"),
                                 boq("90", "Yalıtım levhası 90 mm", "m2")])
        result = insulation.match_item(quoted("Yalıtım levhası 0,075 m", "m²"))
        self.assertEqual(result.item_code, "75")
        self.assertEqual([candidate.item_code for candidate in result.candidates], ["75"])

        panels = BoqMatcher([boq("PANEL", "Alüminyum panel 90 × 210 cm", "adet"),
                             boq("REVERSED", "Alüminyum panel 210 x 90 cm", "adet")])
        result = panels.match_item(quoted("Aluminyum panel 900 mm x 2.1 m.", "pcs"))
        self.assertEqual(result.item_code, "PANEL")
        self.assertEqual([candidate.item_code for candidate in result.candidates], ["PANEL"])

    def test_full_concrete_grades_exact_incomplete_or_conflicting(self):
        matcher = BoqMatcher([boq("C", "Temel için C30/37 hazır beton dökümü", "m³")])
        self.assertEqual(matcher.match_item(quoted("Temel için C-30 / 37 hazır beton dökümü", "m3")).item_code, "C")
        shorthand = matcher.match_item(quoted("Temel için C30 hazır beton dökümü", "m3"))
        self.assertEqual(shorthand.status, "ambiguous")
        self.assertIsNone(shorthand.item_code)
        self.assertEqual(shorthand.candidates[0].unconfirmed_specs, ("concrete grade",))
        conflict = matcher.match_item(quoted("Temel için C30/40 hazır beton dökümü", "m3"))
        self.assertEqual(conflict.status, "unmatched")

    def test_bilingual_description_aliases_still_require_matching_specs(self):
        matcher = BoqMatcher([boq("A", "C28/35 pompa ile hazır beton dökümü", "m3"),
                              boq("B", "C32/40 pompa ile hazır beton dökümü", "m3")])
        result = matcher.match_item(quoted("C28/35 ready-mixed concrete, pump placed", "cubic metres"))

        self.assertEqual(result.item_code, "A")
        self.assertEqual([candidate.item_code for candidate in result.candidates], ["A"])

    def test_missing_width_or_application_keeps_plausible_candidates(self):
        doors = BoqMatcher([boq("A", "85 cm genişliğinde EI90 yangın kapısı", "pcs"),
                            boq("B", "105 cm genişliğinde EI90 yangın kapısı", "pcs")])
        review = doors.match_item(quoted("Fire-rated door EI90", "adet"))
        self.assertEqual(review.status, "ambiguous")
        self.assertEqual([candidate.item_code for candidate in review.candidates], ["A", "B"])
        self.assertTrue(all(candidate.unconfirmed_specs == ("width",) for candidate in review.candidates))
        exact = doors.match_item(quoted("Fire-rated door EI90 width 0.85 m", "pcs"))
        self.assertEqual(exact.item_code, "A")
        wrong_width = doors.match_item(quoted("Fire-rated door EI90 width 95 cm", "pcs"))
        self.assertEqual(wrong_width.status, "unmatched")

        insulation = BoqMatcher([boq("ROOF", "Çatı için mineral yün yalıtımı 60 mm", "m2"),
                                 boq("WALL", "Duvar için mineral yün yalıtımı 60 mm", "m2")])
        review = insulation.match_item(quoted("Mineral wool insulation 6 cm", "m²"))
        self.assertEqual(review.status, "ambiguous")
        self.assertEqual([candidate.item_code for candidate in review.candidates], ["ROOF", "WALL"])
        self.assertEqual(insulation.match_item(quoted("Wall mineral wool insulation 6 cm", "m²")).item_code,
                         "WALL")

    def test_steel_grade_and_fire_rating_conflicts_block_links(self):
        for code, description, equivalent, conflicting in [
            ("S", "B420C çelik donatı çubuğu", "B 420 C çelik donatı çubuğu", "B420A çelik donatı çubuğu"),
            ("F", "EI90 duman sızdırmaz kapı", "EI-90 duman sızdırmaz kapı", "REI90 duman sızdırmaz kapı"),
        ]:
            with self.subTest(code=code):
                matcher = BoqMatcher([boq(code, description, "pcs")])
                self.assertEqual(matcher.match_item(quoted(equivalent, "adet")).item_code, code)
                self.assertEqual(matcher.match_item(quoted(conflicting, "adet")).status, "unmatched")

    def test_ambiguous_candidates_are_reported_without_a_link(self):
        matcher = BoqMatcher([boq("B", "Seramik zemin kaplaması", "m2"),
                              boq("A", "Seramik zemin kaplaması", "m²")])
        result = matcher.match_item(quoted("Seramik zemin kaplaması", "m2"))
        self.assertEqual(result.status, "ambiguous")
        self.assertIsNone(result.item_code)
        self.assertEqual([candidate.item_code for candidate in result.candidates], ["A", "B"])

    def test_rows_stay_in_order_and_missing_values_do_not_link(self):
        matcher = BoqMatcher([boq("P", "Metal levha", "pcs")])
        result = matcher.match_quotation({"extractedItems": [
            quoted("Metal levha", "adet"), quoted("Metal levha", None), quoted("???", "pcs"),
            quoted("Metal levha", "pcs"),
        ]})
        self.assertEqual([row.status for row in result], ["matched", "unmatched", "unmatched", "matched"])
        self.assertEqual([row.item_code for row in result], ["P", None, None, "P"])

    def test_invalid_master_rows_fail_early(self):
        for rows in [[boq("A", "Metal levha", "")], [boq("A", None, "pcs")],
                     [boq("A", "Metal levha", "pcs"), boq("A", "Beton levha", "pcs")]]:
            with self.subTest(rows=rows):
                with self.assertRaises(ValueError):
                    BoqMatcher(rows)


class BenchmarkTests(unittest.TestCase):
    def test_scores_use_automatic_links_for_precision_and_count_abstentions(self):
        report = _summarize([
            {"label": "correct", "expectedItemCodes": ["A"], "prediction": {"status": "matched", "item_code": "A"}},
            {"label": "correct", "expectedItemCodes": ["B"], "prediction": {"status": "ambiguous", "item_code": None}},
            {"label": "ambiguous", "expectedItemCodes": ["C", "D"], "prediction": {"status": "matched", "item_code": "C"}},
            {"label": "unmatched", "expectedItemCodes": [], "prediction": {"status": "ambiguous", "item_code": None}},
        ])
        self.assertEqual(report["top1Accuracy"], {"count": 1, "total": 2, "rate": 0.5})
        self.assertEqual(report["autoMatchPrecision"], {"count": 1, "total": 2, "rate": 0.5})
        self.assertEqual(report["manualReviewRate"], {"count": 2, "total": 4, "rate": 0.5})
        self.assertEqual(report["unmatchedLeftUnlinked"]["count"], 1)
        self.assertEqual(report["unmatchedIdentified"]["count"], 0)

    def test_development_benchmark_covers_all_reviewed_rows(self):
        report = evaluate("development")
        self.assertEqual(report["boqProject"], "SYN-DEV")
        self.assertEqual(report["evaluationPhase"], "development")
        self.assertIs(report["independentEvaluation"], False)
        self.assertEqual(report["metrics"]["correctDecisions"], {"count": 8, "total": 8, "rate": 1.0})
        [review] = [row for row in report["rows"] if row["label"] == "ambiguous"]
        self.assertCountEqual([candidate["item_code"] for candidate in review["prediction"]["candidates"]],
                              review["expectedItemCodes"])

    def test_held_out_directory_is_reported_as_post_adjustment_regression(self):
        report = evaluate("held-out")
        self.assertEqual(report["split"], "held-out")
        self.assertEqual(report["boqProject"], "SYN-HOLD")
        self.assertEqual(report["evaluationPhase"], "post_adjustment_regression")
        self.assertIs(report["independentEvaluation"], False)
