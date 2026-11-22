package extractionbench

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mustDecimal(t *testing.T, text string) Decimal {
	t.Helper()
	value, err := parseDecimal(json.Number(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestDecimalHalfEvenRoundingIncludingDistantOperands(t *testing.T) {
	prefix := "1" + strings.Repeat("0", 48)
	for _, test := range []struct {
		name, left, right string
		subtract          bool
		want              string
	}{
		{"even tie", prefix + "25", "0", false, prefix + "20"},
		{"odd tie", prefix + "35", "0", false, prefix + "40"},
		{"distant positive value tips tie", prefix + "25", "1e-1999999999999999997", false, prefix + "30"},
		{"distant subtraction tips tie", prefix + "35", "1e-1999999999999999997", true, prefix + "30"},
		{"upper exponent with distant subtraction", "1e999999999999999999", "1e-1999999999999999997", true, "1e999999999999999999"},
		{"signed zero cancellation", "-1e1000000", "1e1000000", false, "0"},
		{"smallest half even", "5e-1000000000000000049", "0", false, "0"},
		{"smallest rounds upward", "6e-1000000000000000049", "0", false, "1e-1000000000000000048"},
	} {
		t.Run(test.name, func(t *testing.T) {
			left, right := mustDecimal(t, test.left), mustDecimal(t, test.right)
			var got Decimal
			var err error
			if test.subtract {
				got, err = left.sub(right)
			} else {
				got, err = left.add(right)
			}
			if err != nil {
				t.Fatal(err)
			}
			checkDecimal(t, &got, test.want)
		})
	}
}

func TestDecimalDivisionPrecisionAndExactExponent(t *testing.T) {
	for _, test := range []struct{ dividend, divisor, expected string }{
		{"1", "3", "0." + strings.Repeat("3", 50)},
		{"2", "3", "0." + strings.Repeat("6", 49) + "7"},
		{"2.00", "2", "1.00"},
		{"1.00", "2.00", "0.5"},
		{"1e999999999999999999", "2", "5E+999999999999999998"},
		{"6e-1000000000000000049", "2", "0E-1000000000000000048"},
		{"-0E-20", "2", "-0E-20"},
	} {
		t.Run(test.dividend+"/"+test.divisor, func(t *testing.T) {
			value, err := mustDecimal(t, test.dividend).quo(mustDecimal(t, test.divisor))
			if err != nil || value.String() != test.expected {
				t.Fatalf("expected %s, got %s (%v)", test.expected, value, err)
			}
		})
	}
}

func TestDecimalInputLimitsAndArithmeticOverflow(t *testing.T) {
	for _, number := range []string{"1e999999999999999999", "1e-1999999999999999997", "0.01e1000000000000000000"} {
		mustDecimal(t, number)
	}
	for _, number := range []string{"1e1000000000000000000", "10e999999999999999999", "1e-1999999999999999998"} {
		if _, err := parseDecimal(json.Number(number)); !errors.Is(err, errDecimalRange) {
			t.Fatalf("expected decimal input range error for %s", number)
		}
	}
	large := mustDecimal(t, "9e999999999999999999")
	if _, err := large.add(large); !errors.Is(err, errDecimalRange) {
		t.Fatal("sum overflow should trap")
	}
	if _, err := large.quo(mustDecimal(t, "0.1")); !errors.Is(err, errDecimalRange) {
		t.Fatal("quotient overflow should trap")
	}
}

func TestRelativeErrorKeepsExactDecimalsAndIsUnavailableAtZero(t *testing.T) {
	errRate, err := RelativeError("3.00", "4")
	if err != nil {
		t.Fatal(err)
	}
	checkDecimal(t, errRate, "0."+strings.Repeat("3", 50))
	if errRate, err := RelativeError("-0.00", "4"); err != nil || errRate != nil {
		t.Fatal("relative error at a zero reference must be unavailable")
	}
}
