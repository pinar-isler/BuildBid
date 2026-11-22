package extractionbench

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// The reference uses the 64-bit CPython Decimal bounds, 50 significant digits
// and round-half-even. The exponent is kept separate from the coefficient.
const (
	precision        int64 = 50
	maxDecimalExp    int64 = 999999999999999999
	minInputExp      int64 = -1999999999999999997
	minArithmeticExp       = -maxDecimalExp - precision + 1
)

var errDecimalRange = errors.New("A number exceeds the report's decimal arithmetic range.")

// Decimal is a finite report number. Its zero value represents zero.
type Decimal struct {
	coefficient *big.Int
	exponent    int64
	negative    bool
}

func parseDecimal(input json.Number) (Decimal, error) {
	s := input.String()
	var d Decimal
	s, d.negative = strings.CutPrefix(s, "-")
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, err := strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil || exponent < minInputExp || exponent > 2*maxDecimalExp {
			return Decimal{}, errDecimalRange
		}
		d.exponent, s = exponent, s[:i]
	}
	integer, fraction, _ := strings.Cut(s, ".")
	coefficient, ok := new(big.Int).SetString(integer+fraction, 10)
	if !ok || coefficient.Sign() < 0 {
		return Decimal{}, errors.New("invalid report number")
	}
	d.coefficient, d.exponent = coefficient, d.exponent-int64(len(fraction))
	if d.exponent < minInputExp || d.adjusted() > maxDecimalExp {
		return Decimal{}, errDecimalRange
	}
	if d.zero() && !strings.ContainsAny(input.String(), ".eE") {
		d.negative = false // Python parses integer -0 as 0.
	}
	return d, nil
}

func decimalInt(n int64) Decimal {
	magnitude := big.NewInt(n)
	return Decimal{coefficient: magnitude.Abs(magnitude), negative: n < 0}
}

func (d Decimal) zero() bool {
	return d.coefficient == nil || d.coefficient.Sign() == 0
}

func (d Decimal) digits() string {
	if d.zero() {
		return "0"
	}
	return d.coefficient.String()
}

func (d Decimal) adjusted() int64 { return d.exponent + int64(len(d.digits())) - 1 }

func (d Decimal) abs() Decimal {
	d.negative = false
	return d
}

func (d Decimal) String() string {
	digits := d.digits()
	adjusted := d.exponent + int64(len(digits)) - 1
	sign := ""
	if d.negative {
		sign = "-"
	}
	if d.exponent == 0 {
		return sign + digits
	}
	if d.exponent < 0 && adjusted >= -6 {
		point := int64(len(digits)) + d.exponent
		if point <= 0 {
			return sign + "0." + strings.Repeat("0", int(-point)) + digits
		}
		return sign + digits[:point] + "." + digits[point:]
	}
	if len(digits) > 1 {
		digits = digits[:1] + "." + digits[1:]
	}
	exponent := strconv.FormatInt(adjusted, 10)
	if adjusted >= 0 {
		exponent = "+" + exponent
	}
	return sign + digits + "E" + exponent
}

func (d Decimal) MarshalJSON() ([]byte, error) { return []byte(d.String()), nil }

func (d Decimal) cmp(other Decimal) int {
	if d.zero() && other.zero() {
		return 0
	}
	if d.negative != other.negative {
		if d.negative {
			return -1
		}
		return 1
	}
	order := d.compareMagnitude(other)
	if d.negative {
		return -order
	}
	return order
}

func (d Decimal) compareMagnitude(other Decimal) int {
	if d.zero() {
		if other.zero() {
			return 0
		}
		return -1
	}
	if other.zero() {
		return 1
	}
	left, right := d.adjusted(), other.adjusted()
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return strings.Compare(strings.TrimRight(d.digits(), "0"), strings.TrimRight(other.digits(), "0"))
	}
}

func (d Decimal) sub(other Decimal) (Decimal, error) {
	other.negative = !other.negative
	return d.add(other)
}

func (d Decimal) add(other Decimal) (Decimal, error) {
	if d.compareMagnitude(other) < 0 {
		d, other = other, d
	}
	if d.zero() {
		return Decimal{
			exponent: min(d.exponent, other.exponent),
			negative: d.negative && other.negative,
		}.round()
	}
	// Keep the sign of any distant nonzero operand strictly below the
	// rounding position. It can tip an exact tie without requiring that
	// either coefficient grow by the full exponent gap.
	cutoff := min(d.exponent-1, d.adjusted()-precision-2)
	if other.exponent < cutoff && (other.zero() || other.adjusted() < cutoff) {
		if !other.zero() {
			other.coefficient = big.NewInt(1)
		}
		other.exponent = cutoff
	}
	exponent := min(d.exponent, other.exponent)
	a, b := d.signedCoefficient(exponent), other.signedCoefficient(exponent)
	sum := a.Add(a, b)
	negative := sum.Sign() < 0
	return Decimal{sum.Abs(sum), exponent, negative}.round()
}

func (d Decimal) signedCoefficient(exponent int64) *big.Int {
	if d.zero() {
		return new(big.Int)
	}
	value := scaled(d.coefficient, d.exponent-exponent)
	if d.negative {
		value.Neg(value)
	}
	return value
}

func (d Decimal) round() (Decimal, error) {
	if d.zero() {
		d.exponent = min(maxDecimalExp, max(minArithmeticExp, d.exponent))
		return d, nil
	}
	length := int64(len(d.digits()))
	target := max(d.exponent, d.exponent+length-precision, minArithmeticExp)
	shift := target - d.exponent
	if shift > length {
		return Decimal{exponent: target, negative: d.negative}, nil
	}
	if shift > 0 {
		divisor := power10(shift)
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(d.coefficient, divisor, remainder)
		roundQuotient(quotient, remainder, divisor)
		d.coefficient, d.exponent = quotient, target
	}
	return d.finish()
}

func (d Decimal) quo(other Decimal) (Decimal, error) {
	if other.zero() {
		return Decimal{}, errors.New("report decimal division by zero")
	}
	preferred := d.exponent - other.exponent
	negative := d.negative != other.negative
	if d.zero() {
		return Decimal{exponent: preferred, negative: negative}.round()
	}
	lengthGap := int64(len(d.digits())) - int64(len(other.digits()))
	left, right := d.coefficient, other.coefficient
	if lengthGap >= 0 {
		right = scaled(right, lengthGap)
	} else {
		left = scaled(left, -lengthGap)
	}
	adjusted := preferred + lengthGap
	if left.Cmp(right) < 0 {
		adjusted--
	}
	if adjusted > maxDecimalExp {
		return Decimal{}, errDecimalRange
	}
	if adjusted < minArithmeticExp-1 {
		return Decimal{exponent: minArithmeticExp, negative: negative}, nil
	}
	target := max(adjusted-precision+1, minArithmeticExp)
	left, right = d.coefficient, other.coefficient
	if shift := preferred - target; shift >= 0 {
		left = scaled(left, shift)
	} else {
		right = scaled(right, -shift)
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(left, right, remainder)
	if remainder.Sign() == 0 && target < preferred {
		// An exact quotient keeps the preferred exponent when possible.
		digits := quotient.String()
		trailing := int64(len(digits) - len(strings.TrimRight(digits, "0")))
		shift := min(trailing, preferred-target)
		quotient.Quo(quotient, power10(shift))
		target += shift
	} else {
		roundQuotient(quotient, remainder, right)
	}
	return Decimal{quotient, target, negative}.finish()
}

func (d Decimal) finish() (Decimal, error) {
	if !d.zero() && int64(len(d.digits())) > precision {
		d.coefficient = new(big.Int).Quo(d.coefficient, big.NewInt(10))
		d.exponent++
	}
	if d.adjusted() > maxDecimalExp {
		return Decimal{}, errDecimalRange
	}
	return d, nil
}

func roundQuotient(quotient, remainder, divisor *big.Int) {
	half := new(big.Int).Lsh(remainder, 1).Cmp(divisor)
	if half > 0 || (half == 0 && quotient.Bit(0) == 1) {
		quotient.Add(quotient, big.NewInt(1))
	}
}

func scaled(value *big.Int, shift int64) *big.Int {
	if shift == 0 {
		return new(big.Int).Set(value)
	}
	return new(big.Int).Mul(value, power10(shift))
}

func power10(exponent int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil)
}

// RelativeError returns |predicted-expected|/|expected| under the same
// arithmetic as the benchmark. A zero reference value has no relative error.
// The frozen benchmark report continues to publish absolute errors.
func RelativeError(expected, predicted json.Number) (*Decimal, error) {
	base, err := parseDecimal(expected)
	if err != nil {
		return nil, err
	}
	actual, err := parseDecimal(predicted)
	if err != nil || base.zero() {
		return nil, err
	}
	delta, err := actual.sub(base)
	if err != nil {
		return nil, err
	}
	result, err := delta.abs().quo(base.abs())
	if err != nil {
		return nil, err
	}
	return &result, nil
}
