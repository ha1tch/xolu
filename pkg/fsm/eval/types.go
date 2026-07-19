// Package tsqlruntime provides a runtime interpreter for T-SQL,
// enabling execution of dynamic SQL at runtime in Go applications.
package eval

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// DataType represents T-SQL data types
type DataType int

const (
	TypeUnknown DataType = iota
	// Integer types
	TypeBit
	TypeTinyInt
	TypeSmallInt
	TypeInt
	TypeBigInt
	// Exact numeric
	TypeDecimal
	TypeNumeric
	TypeMoney
	TypeSmallMoney
	// Approximate numeric
	TypeFloat
	TypeReal
	// Date/time
	TypeDate
	TypeTime
	TypeDateTime
	TypeDateTime2
	TypeSmallDateTime
	TypeDateTimeOffset
	// String
	TypeChar
	TypeVarChar
	TypeNChar
	TypeNVarChar
	TypeText
	TypeNText
	// Binary
	TypeBinary
	TypeVarBinary
	// Other
	TypeUniqueIdentifier
	TypeXML
	TypeTable
)

func (dt DataType) String() string {
	names := map[DataType]string{
		TypeUnknown:          "unknown",
		TypeBit:              "bit",
		TypeTinyInt:          "tinyint",
		TypeSmallInt:         "smallint",
		TypeInt:              "int",
		TypeBigInt:           "bigint",
		TypeDecimal:          "decimal",
		TypeNumeric:          "numeric",
		TypeMoney:            "money",
		TypeSmallMoney:       "smallmoney",
		TypeFloat:            "float",
		TypeReal:             "real",
		TypeDate:             "date",
		TypeTime:             "time",
		TypeDateTime:         "datetime",
		TypeDateTime2:        "datetime2",
		TypeSmallDateTime:    "smalldatetime",
		TypeDateTimeOffset:   "datetimeoffset",
		TypeChar:             "char",
		TypeVarChar:          "varchar",
		TypeNChar:            "nchar",
		TypeNVarChar:         "nvarchar",
		TypeText:             "text",
		TypeNText:            "ntext",
		TypeBinary:           "binary",
		TypeVarBinary:        "varbinary",
		TypeUniqueIdentifier: "uniqueidentifier",
		TypeXML:              "xml",
		TypeTable:            "table",
	}
	if name, ok := names[dt]; ok {
		return name
	}
	return "unknown"
}

// IsNumeric returns true if the type is numeric
func (dt DataType) IsNumeric() bool {
	switch dt {
	case TypeBit, TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt,
		TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney,
		TypeFloat, TypeReal:
		return true
	}
	return false
}

// IsString returns true if the type is a string type
func (dt DataType) IsString() bool {
	switch dt {
	case TypeChar, TypeVarChar, TypeNChar, TypeNVarChar, TypeText, TypeNText:
		return true
	}
	return false
}

// IsDateTime returns true if the type is a date/time type
func (dt DataType) IsDateTime() bool {
	switch dt {
	case TypeDate, TypeTime, TypeDateTime, TypeDateTime2,
		TypeSmallDateTime, TypeDateTimeOffset:
		return true
	}
	return false
}

// IsInteger returns true if the type is an integer type
func (dt DataType) IsInteger() bool {
	switch dt {
	case TypeBit, TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return true
	}
	return false
}

// Value represents a runtime T-SQL value with type information
type Value struct {
	Type      DataType
	IsNull    bool
	Precision int // For decimal/numeric
	Scale     int // For decimal/numeric
	MaxLen    int // For string/binary types

	// Storage - only one of these is used based on Type
	intVal     int64
	floatVal   float64
	decimalVal decimal.Decimal
	stringVal  string
	boolVal    bool
	timeVal    time.Time
	bytesVal   []byte
}

// Null returns a NULL value of the given type
func Null(dt DataType) Value {
	return Value{Type: dt, IsNull: true}
}

// NewInt creates an integer value
func NewInt(v int64) Value {
	return Value{Type: TypeInt, intVal: v}
}

// NewBigInt creates a bigint value
func NewBigInt(v int64) Value {
	return Value{Type: TypeBigInt, intVal: v}
}

// NewSmallInt creates a smallint value
func NewSmallInt(v int16) Value {
	return Value{Type: TypeSmallInt, intVal: int64(v)}
}

// NewTinyInt creates a tinyint value
func NewTinyInt(v uint8) Value {
	return Value{Type: TypeTinyInt, intVal: int64(v)}
}

// NewBit creates a bit value
func NewBit(v bool) Value {
	val := int64(0)
	if v {
		val = 1
	}
	return Value{Type: TypeBit, intVal: val, boolVal: v}
}

// NewFloat creates a float value
func NewFloat(v float64) Value {
	return Value{Type: TypeFloat, floatVal: v}
}

// NewReal creates a real value
func NewReal(v float32) Value {
	return Value{Type: TypeReal, floatVal: float64(v)}
}

// NewDecimal creates a decimal value
func NewDecimal(v decimal.Decimal, precision, scale int) Value {
	return Value{Type: TypeDecimal, decimalVal: v, Precision: precision, Scale: scale}
}

// NewDecimalFromString creates a decimal from a string
func NewDecimalFromString(s string, precision, scale int) (Value, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return Value{}, err
	}
	return Value{Type: TypeDecimal, decimalVal: d, Precision: precision, Scale: scale}, nil
}

// NewMoney creates a money value
func NewMoney(v decimal.Decimal) Value {
	return Value{Type: TypeMoney, decimalVal: v, Precision: 19, Scale: 4}
}

// NewVarChar creates a varchar value
func NewVarChar(v string, maxLen int) Value {
	if maxLen > 0 && len(v) > maxLen {
		v = v[:maxLen]
	}
	return Value{Type: TypeVarChar, stringVal: v, MaxLen: maxLen}
}

// NewNVarChar creates an nvarchar value
func NewNVarChar(v string, maxLen int) Value {
	if maxLen > 0 && len([]rune(v)) > maxLen {
		v = string([]rune(v)[:maxLen])
	}
	return Value{Type: TypeNVarChar, stringVal: v, MaxLen: maxLen}
}

// NewChar creates a char value (padded to length)
func NewChar(v string, length int) Value {
	if len(v) < length {
		v = v + strings.Repeat(" ", length-len(v))
	} else if len(v) > length {
		v = v[:length]
	}
	return Value{Type: TypeChar, stringVal: v, MaxLen: length}
}

// NewDateTime creates a datetime value
func NewDateTime(v time.Time) Value {
	return Value{Type: TypeDateTime, timeVal: v}
}

// NewDate creates a date value
func NewDate(v time.Time) Value {
	// Strip time portion
	y, m, d := v.Date()
	return Value{Type: TypeDate, timeVal: time.Date(y, m, d, 0, 0, 0, 0, v.Location())}
}

// NewTime creates a time value
func NewTime(v time.Time) Value {
	return Value{Type: TypeTime, timeVal: v}
}

// NewBinary creates a binary value
func NewBinary(v []byte) Value {
	return Value{Type: TypeVarBinary, bytesVal: v}
}

// NewVarBinary creates a new varbinary value
func NewVarBinary(v []byte, maxLen int) Value {
	return Value{Type: TypeVarBinary, bytesVal: v, MaxLen: maxLen}
}

// AsInt returns the value as int64, with type coercion
func (v Value) AsInt() int64 {
	if v.IsNull {
		return 0
	}
	switch v.Type {
	case TypeBit, TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return v.intVal
	case TypeFloat, TypeReal:
		return int64(v.floatVal)
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return v.decimalVal.IntPart()
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		i, _ := strconv.ParseInt(strings.TrimSpace(v.stringVal), 10, 64)
		return i
	}
	return 0
}

// AsFloat returns the value as float64, with type coercion
func (v Value) AsFloat() float64 {
	if v.IsNull {
		return 0
	}
	switch v.Type {
	case TypeFloat, TypeReal:
		return v.floatVal
	case TypeBit, TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return float64(v.intVal)
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		f, _ := v.decimalVal.Float64()
		return f
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v.stringVal), 64)
		return f
	}
	return 0
}

// AsDecimal returns the value as decimal.Decimal, with type coercion
func (v Value) AsDecimal() decimal.Decimal {
	if v.IsNull {
		return decimal.Zero
	}
	switch v.Type {
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return v.decimalVal
	case TypeBit, TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return decimal.NewFromInt(v.intVal)
	case TypeFloat, TypeReal:
		// shopspring/decimal.NewFromFloat panics on NaN, +Inf, -Inf.
		// A non-finite value cannot be represented as a fixed-precision
		// decimal, so map it to zero rather than crashing the evaluator.
		// Callers that need to distinguish "not-a-number" from a legitimate
		// zero should test IsFinite() themselves before calling AsDecimal.
		if math.IsNaN(v.floatVal) || math.IsInf(v.floatVal, 0) {
			return decimal.Zero
		}
		return decimal.NewFromFloat(v.floatVal)
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		d, _ := decimal.NewFromString(strings.TrimSpace(v.stringVal))
		return d
	}
	return decimal.Zero
}

// AsString returns the value as string, with type coercion
func (v Value) AsString() string {
	if v.IsNull {
		return ""
	}
	switch v.Type {
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar, TypeText, TypeNText:
		return v.stringVal
	case TypeBit:
		if v.boolVal || v.intVal != 0 {
			return "1"
		}
		return "0"
	case TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return strconv.FormatInt(v.intVal, 10)
	case TypeFloat, TypeReal:
		return strconv.FormatFloat(v.floatVal, 'g', -1, 64)
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return v.decimalVal.String()
	case TypeDateTime, TypeDateTime2, TypeSmallDateTime:
		return v.timeVal.Format("2006-01-02 15:04:05")
	case TypeDate:
		return v.timeVal.Format("2006-01-02")
	case TypeTime:
		return v.timeVal.Format("15:04:05")
	case TypeUniqueIdentifier:
		return v.stringVal
	}
	return fmt.Sprintf("%v", v)
}

// AsBool returns the value as bool, with type coercion
func (v Value) AsBool() bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case TypeBit:
		return v.boolVal || v.intVal != 0
	case TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return v.intVal != 0
	case TypeFloat, TypeReal:
		return v.floatVal != 0
	case TypeDecimal, TypeNumeric:
		return !v.decimalVal.IsZero()
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		s := strings.TrimSpace(strings.ToLower(v.stringVal))
		return s == "true" || s == "1" || s == "yes"
	}
	return false
}

// AsTime returns the value as time.Time, with type coercion
func (v Value) AsTime() time.Time {
	if v.IsNull {
		return time.Time{}
	}
	switch v.Type {
	case TypeDate, TypeTime, TypeDateTime, TypeDateTime2, TypeSmallDateTime, TypeDateTimeOffset:
		return v.timeVal
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		// Try common formats
		formats := []string{
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			"2006-01-02",
			"01/02/2006",
			"02/01/2006",
			"Jan 2, 2006",
			"2 Jan 2006",
		}
		s := strings.TrimSpace(v.stringVal)
		for _, fmt := range formats {
			if t, err := time.Parse(fmt, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// Compare compares two values, returning -1, 0, or 1
func (v Value) Compare(other Value) int {
	// NULL handling: NULL is not equal to anything, including NULL
	if v.IsNull || other.IsNull {
		return 0 // Special case - caller should check IsNull
	}

	// Numeric comparison
	if v.Type.IsNumeric() && other.Type.IsNumeric() {
		d1 := v.AsDecimal()
		d2 := other.AsDecimal()
		return d1.Cmp(d2)
	}

	// String comparison
	if v.Type.IsString() && other.Type.IsString() {
		return strings.Compare(v.stringVal, other.stringVal)
	}

	// DateTime comparison
	if v.Type.IsDateTime() && other.Type.IsDateTime() {
		t1 := v.AsTime()
		t2 := other.AsTime()
		if t1.Before(t2) {
			return -1
		}
		if t1.After(t2) {
			return 1
		}
		return 0
	}

	// Mixed type - convert to string for comparison
	return strings.Compare(v.AsString(), other.AsString())
}

// IsTruthy returns true if the value is considered "true" in a boolean context
func (v Value) IsTruthy() bool {
	if v.IsNull {
		return false
	}
	return v.AsBool()
}

// Clone creates a copy of the value
func (v Value) Clone() Value {
	clone := v
	if v.bytesVal != nil {
		clone.bytesVal = make([]byte, len(v.bytesVal))
		copy(clone.bytesVal, v.bytesVal)
	}
	return clone
}

// ToInterface converts the Value to a Go interface{} for use with JSON/XML
func (v Value) ToInterface() interface{} {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case TypeBit:
		return v.boolVal
	case TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return v.intVal
	case TypeFloat, TypeReal:
		return v.floatVal
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		f, _ := v.decimalVal.Float64()
		return f
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar, TypeXML:
		return v.stringVal
	case TypeDateTime, TypeDate, TypeTime, TypeDateTime2, TypeSmallDateTime, TypeDateTimeOffset:
		return v.timeVal
	case TypeVarBinary, TypeBinary:
		return v.bytesVal
	default:
		return v.AsString()
	}
}

// Equals checks if two values are equal (handles NULL)
func (v Value) Equals(other Value) Value {
	// In SQL, NULL = NULL is NULL (unknown), not true
	if v.IsNull || other.IsNull {
		return Null(TypeBit)
	}
	return NewBit(v.Compare(other) == 0)
}

// NotEquals checks if two values are not equal
func (v Value) NotEquals(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBit)
	}
	return NewBit(v.Compare(other) != 0)
}

// LessThan compares values
func (v Value) LessThan(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBit)
	}
	return NewBit(v.Compare(other) < 0)
}

// LessThanOrEqual compares values
func (v Value) LessThanOrEqual(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBit)
	}
	return NewBit(v.Compare(other) <= 0)
}

// GreaterThan compares values
func (v Value) GreaterThan(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBit)
	}
	return NewBit(v.Compare(other) > 0)
}

// GreaterThanOrEqual compares values
func (v Value) GreaterThanOrEqual(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBit)
	}
	return NewBit(v.Compare(other) >= 0)
}

// Add performs addition
func (v Value) Add(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(inferNumericType(v.Type, other.Type))
	}

	// String concatenation
	if v.Type.IsString() || other.Type.IsString() {
		return NewVarChar(v.AsString()+other.AsString(), -1)
	}

	// Decimal arithmetic
	if v.Type == TypeDecimal || v.Type == TypeNumeric ||
		other.Type == TypeDecimal || other.Type == TypeNumeric ||
		v.Type == TypeMoney || other.Type == TypeMoney {
		return NewDecimal(v.AsDecimal().Add(other.AsDecimal()), 38, 10)
	}

	// Float arithmetic
	if v.Type == TypeFloat || v.Type == TypeReal ||
		other.Type == TypeFloat || other.Type == TypeReal {
		return NewFloat(v.AsFloat() + other.AsFloat())
	}

	// Integer arithmetic
	return NewBigInt(v.AsInt() + other.AsInt())
}

// Sub performs subtraction
func (v Value) Sub(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(inferNumericType(v.Type, other.Type))
	}

	if v.Type == TypeDecimal || v.Type == TypeNumeric ||
		other.Type == TypeDecimal || other.Type == TypeNumeric ||
		v.Type == TypeMoney || other.Type == TypeMoney {
		return NewDecimal(v.AsDecimal().Sub(other.AsDecimal()), 38, 10)
	}

	if v.Type == TypeFloat || v.Type == TypeReal ||
		other.Type == TypeFloat || other.Type == TypeReal {
		return NewFloat(v.AsFloat() - other.AsFloat())
	}

	return NewBigInt(v.AsInt() - other.AsInt())
}

// Mul performs multiplication
func (v Value) Mul(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(inferNumericType(v.Type, other.Type))
	}

	if v.Type == TypeDecimal || v.Type == TypeNumeric ||
		other.Type == TypeDecimal || other.Type == TypeNumeric ||
		v.Type == TypeMoney || other.Type == TypeMoney {
		return NewDecimal(v.AsDecimal().Mul(other.AsDecimal()), 38, 10)
	}

	if v.Type == TypeFloat || v.Type == TypeReal ||
		other.Type == TypeFloat || other.Type == TypeReal {
		return NewFloat(v.AsFloat() * other.AsFloat())
	}

	return NewBigInt(v.AsInt() * other.AsInt())
}

// Div performs division
func (v Value) Div(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(inferNumericType(v.Type, other.Type))
	}

	// Check for division by zero
	if other.AsDecimal().IsZero() {
		return Null(TypeDecimal) // SQL Server returns NULL for divide by zero
	}

	if v.Type == TypeDecimal || v.Type == TypeNumeric ||
		other.Type == TypeDecimal || other.Type == TypeNumeric ||
		v.Type == TypeMoney || other.Type == TypeMoney {
		return NewDecimal(v.AsDecimal().Div(other.AsDecimal()), 38, 10)
	}

	if v.Type == TypeFloat || v.Type == TypeReal ||
		other.Type == TypeFloat || other.Type == TypeReal {
		return NewFloat(v.AsFloat() / other.AsFloat())
	}

	// Integer division
	return NewBigInt(v.AsInt() / other.AsInt())
}

// Mod performs modulo
func (v Value) Mod(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(inferNumericType(v.Type, other.Type))
	}

	if other.AsInt() == 0 {
		return Null(TypeInt)
	}

	return NewBigInt(v.AsInt() % other.AsInt())
}

// Neg negates the value
func (v Value) Neg() Value {
	if v.IsNull {
		return v
	}

	switch v.Type {
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return NewDecimal(v.decimalVal.Neg(), v.Precision, v.Scale)
	case TypeFloat, TypeReal:
		return NewFloat(-v.floatVal)
	default:
		return NewBigInt(-v.intVal)
	}
}

// And performs logical AND
func (v Value) And(other Value) Value {
	if v.IsNull || other.IsNull {
		// SQL three-valued logic: NULL AND FALSE = FALSE, NULL AND TRUE = NULL
		if !v.IsNull && !v.AsBool() {
			return NewBit(false)
		}
		if !other.IsNull && !other.AsBool() {
			return NewBit(false)
		}
		return Null(TypeBit)
	}
	return NewBit(v.AsBool() && other.AsBool())
}

// Or performs logical OR
func (v Value) Or(other Value) Value {
	if v.IsNull || other.IsNull {
		// SQL three-valued logic: NULL OR TRUE = TRUE, NULL OR FALSE = NULL
		if !v.IsNull && v.AsBool() {
			return NewBit(true)
		}
		if !other.IsNull && other.AsBool() {
			return NewBit(true)
		}
		return Null(TypeBit)
	}
	return NewBit(v.AsBool() || other.AsBool())
}

// Not performs logical NOT
func (v Value) Not() Value {
	if v.IsNull {
		return Null(TypeBit)
	}
	return NewBit(!v.AsBool())
}

// BitwiseAnd performs bitwise AND
func (v Value) BitwiseAnd(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBigInt)
	}
	return NewBigInt(v.AsInt() & other.AsInt())
}

// BitwiseOr performs bitwise OR
func (v Value) BitwiseOr(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBigInt)
	}
	return NewBigInt(v.AsInt() | other.AsInt())
}

// BitwiseXor performs bitwise XOR
func (v Value) BitwiseXor(other Value) Value {
	if v.IsNull || other.IsNull {
		return Null(TypeBigInt)
	}
	return NewBigInt(v.AsInt() ^ other.AsInt())
}

// BitwiseNot performs bitwise NOT
func (v Value) BitwiseNot() Value {
	if v.IsNull {
		return Null(TypeBigInt)
	}
	return NewBigInt(^v.AsInt())
}

// inferNumericType determines the result type for numeric operations
func inferNumericType(t1, t2 DataType) DataType {
	// Precedence: decimal > float > bigint > int > smallint > tinyint
	if t1 == TypeDecimal || t1 == TypeNumeric || t2 == TypeDecimal || t2 == TypeNumeric {
		return TypeDecimal
	}
	if t1 == TypeMoney || t2 == TypeMoney {
		return TypeMoney
	}
	if t1 == TypeFloat || t2 == TypeFloat {
		return TypeFloat
	}
	if t1 == TypeReal || t2 == TypeReal {
		return TypeReal
	}
	if t1 == TypeBigInt || t2 == TypeBigInt {
		return TypeBigInt
	}
	return TypeInt
}

// Power raises v to the power of exp
func (v Value) Power(exp Value) Value {
	if v.IsNull || exp.IsNull {
		return Null(TypeFloat)
	}
	return NewFloat(math.Pow(v.AsFloat(), exp.AsFloat()))
}
