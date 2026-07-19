package eval

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Cast converts a value to the target type
func Cast(v Value, targetType DataType, precision, scale, maxLen int) (Value, error) {
	return Convert(v, targetType, precision, scale, maxLen, 0)
}

// Convert converts a value to the target type with optional style
func Convert(v Value, targetType DataType, precision, scale, maxLen int, style int) (Value, error) {
	if v.IsNull {
		return Null(targetType), nil
	}

	// D-017: a declared length (e.g. CAST(x AS CHAR(n))) drives fixed-width
	// padding allocations in NewChar/convertToBinary. A guard expression must not
	// be able to request an arbitrarily large allocation, so bound the declared
	// length to the same limit as function output. maxLen == -1 is MAX (no
	// fixed-width padding) and is left alone.
	if maxLen > maxFunctionOutputBytes {
		return Value{}, fmt.Errorf("CAST/CONVERT length %d exceeds limit %d", maxLen, maxFunctionOutputBytes)
	}

	switch targetType {
	case TypeBit:
		return convertToBit(v)
	case TypeTinyInt:
		return convertToTinyInt(v)
	case TypeSmallInt:
		return convertToSmallInt(v)
	case TypeInt:
		return convertToInt(v)
	case TypeBigInt:
		return convertToBigInt(v)
	case TypeDecimal, TypeNumeric:
		return convertToDecimal(v, precision, scale)
	case TypeMoney:
		return convertToMoney(v)
	case TypeSmallMoney:
		return convertToSmallMoney(v)
	case TypeFloat:
		return convertToFloat(v)
	case TypeReal:
		return convertToReal(v)
	case TypeDate:
		return convertToDate(v, style)
	case TypeTime:
		return convertToTime(v, style)
	case TypeDateTime, TypeDateTime2:
		return convertToDateTime(v, style)
	case TypeSmallDateTime:
		return convertToSmallDateTime(v, style)
	case TypeChar:
		return convertToChar(v, maxLen, style)
	case TypeVarChar:
		return convertToVarChar(v, maxLen, style)
	case TypeNChar:
		return convertToNChar(v, maxLen, style)
	case TypeNVarChar:
		return convertToNVarChar(v, maxLen, style)
	case TypeBinary, TypeVarBinary:
		return convertToBinary(v, maxLen)
	default:
		return Value{}, fmt.Errorf("conversion to %s not supported", targetType)
	}
}

func convertToBit(v Value) (Value, error) {
	switch v.Type {
	case TypeBit:
		return v, nil
	case TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return NewBit(v.intVal != 0), nil
	case TypeFloat, TypeReal:
		return NewBit(v.floatVal != 0), nil
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return NewBit(!v.decimalVal.IsZero()), nil
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		s := strings.TrimSpace(strings.ToLower(v.stringVal))
		if s == "true" || s == "1" {
			return NewBit(true), nil
		}
		if s == "false" || s == "0" {
			return NewBit(false), nil
		}
		return Value{}, fmt.Errorf("cannot convert '%s' to bit", v.stringVal)
	}
	return Value{}, fmt.Errorf("cannot convert %s to bit", v.Type)
}

func convertToTinyInt(v Value) (Value, error) {
	i := v.AsInt()
	if i < 0 || i > 255 {
		return Value{}, fmt.Errorf("arithmetic overflow converting to tinyint")
	}
	return NewTinyInt(uint8(i)), nil
}

func convertToSmallInt(v Value) (Value, error) {
	i := v.AsInt()
	if i < -32768 || i > 32767 {
		return Value{}, fmt.Errorf("arithmetic overflow converting to smallint")
	}
	return NewSmallInt(int16(i)), nil
}

func convertToInt(v Value) (Value, error) {
	i := v.AsInt()
	if i < -2147483648 || i > 2147483647 {
		return Value{}, fmt.Errorf("arithmetic overflow converting to int")
	}
	return NewInt(i), nil
}

func convertToBigInt(v Value) (Value, error) {
	return NewBigInt(v.AsInt()), nil
}

func convertToDecimal(v Value, precision, scale int) (Value, error) {
	if precision == 0 {
		precision = 18
	}
	if scale == 0 {
		scale = 0
	}
	d := v.AsDecimal()
	// Round to specified scale
	d = d.Round(int32(scale))
	return NewDecimal(d, precision, scale), nil
}

func convertToMoney(v Value) (Value, error) {
	d := v.AsDecimal().Round(4)
	return NewMoney(d), nil
}

func convertToSmallMoney(v Value) (Value, error) {
	d := v.AsDecimal().Round(4)
	// Check range: -214,748.3648 to 214,748.3647
	max := decimal.NewFromFloat(214748.3647)
	min := decimal.NewFromFloat(-214748.3648)
	if d.GreaterThan(max) || d.LessThan(min) {
		return Value{}, fmt.Errorf("arithmetic overflow converting to smallmoney")
	}
	return Value{Type: TypeSmallMoney, decimalVal: d, Precision: 10, Scale: 4}, nil
}

func convertToFloat(v Value) (Value, error) {
	return NewFloat(v.AsFloat()), nil
}

func convertToReal(v Value) (Value, error) {
	return NewReal(float32(v.AsFloat())), nil
}

func convertToDate(v Value, style int) (Value, error) {
	switch v.Type {
	case TypeDate:
		return v, nil
	case TypeDateTime, TypeDateTime2, TypeSmallDateTime:
		return NewDate(v.timeVal), nil
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		t, err := parseDateTimeWithStyle(v.stringVal, style)
		if err != nil {
			return Value{}, err
		}
		return NewDate(t), nil
	}
	return Value{}, fmt.Errorf("cannot convert %s to date", v.Type)
}

func convertToTime(v Value, style int) (Value, error) {
	switch v.Type {
	case TypeTime:
		return v, nil
	case TypeDateTime, TypeDateTime2, TypeSmallDateTime:
		return NewTime(v.timeVal), nil
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		t, err := parseDateTimeWithStyle(v.stringVal, style)
		if err != nil {
			return Value{}, err
		}
		return NewTime(t), nil
	}
	return Value{}, fmt.Errorf("cannot convert %s to time", v.Type)
}

func convertToDateTime(v Value, style int) (Value, error) {
	switch v.Type {
	case TypeDateTime, TypeDateTime2:
		return v, nil
	case TypeDate:
		return NewDateTime(v.timeVal), nil
	case TypeSmallDateTime:
		return NewDateTime(v.timeVal), nil
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		t, err := parseDateTimeWithStyle(v.stringVal, style)
		if err != nil {
			return Value{}, err
		}
		return NewDateTime(t), nil
	case TypeInt, TypeBigInt:
		// Days since 1900-01-01
		base := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
		return NewDateTime(base.AddDate(0, 0, int(v.intVal))), nil
	}
	return Value{}, fmt.Errorf("cannot convert %s to datetime", v.Type)
}

func convertToSmallDateTime(v Value, style int) (Value, error) {
	dt, err := convertToDateTime(v, style)
	if err != nil {
		return Value{}, err
	}
	// SmallDateTime rounds to nearest minute
	t := dt.timeVal
	t = t.Truncate(time.Minute)
	return Value{Type: TypeSmallDateTime, timeVal: t}, nil
}

func convertToChar(v Value, maxLen int, style int) (Value, error) {
	s, err := formatValueAsString(v, style)
	if err != nil {
		return Value{}, err
	}
	return NewChar(s, maxLen), nil
}

func convertToVarChar(v Value, maxLen int, style int) (Value, error) {
	s, err := formatValueAsString(v, style)
	if err != nil {
		return Value{}, err
	}
	return NewVarChar(s, maxLen), nil
}

func convertToNChar(v Value, maxLen int, style int) (Value, error) {
	s, err := formatValueAsString(v, style)
	if err != nil {
		return Value{}, err
	}
	return NewChar(s, maxLen), nil
}

func convertToNVarChar(v Value, maxLen int, style int) (Value, error) {
	s, err := formatValueAsString(v, style)
	if err != nil {
		return Value{}, err
	}
	return NewNVarChar(s, maxLen), nil
}

func convertToBinary(v Value, maxLen int) (Value, error) {
	switch v.Type {
	case TypeBinary, TypeVarBinary:
		b := v.bytesVal
		if maxLen > 0 && len(b) > maxLen {
			b = b[:maxLen]
		}
		return NewBinary(b), nil
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		b := []byte(v.stringVal)
		if maxLen > 0 && len(b) > maxLen {
			b = b[:maxLen]
		}
		return NewBinary(b), nil
	case TypeInt, TypeBigInt:
		// Convert integer to binary representation
		b := make([]byte, 8)
		val := v.intVal
		for i := 7; i >= 0; i-- {
			b[i] = byte(val & 0xFF)
			val >>= 8
		}
		if maxLen > 0 && len(b) > maxLen {
			b = b[len(b)-maxLen:]
		}
		return NewBinary(b), nil
	}
	return Value{}, fmt.Errorf("cannot convert %s to binary", v.Type)
}

// formatValueAsString formats a value as a string using the given style
func formatValueAsString(v Value, style int) (string, error) {
	switch v.Type {
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar, TypeText, TypeNText:
		return v.stringVal, nil

	case TypeBit:
		if v.boolVal || v.intVal != 0 {
			return "1", nil
		}
		return "0", nil

	case TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return strconv.FormatInt(v.intVal, 10), nil

	case TypeFloat, TypeReal:
		if style == 0 {
			return strconv.FormatFloat(v.floatVal, 'g', -1, 64), nil
		}
		// Style 1: 8 digits scientific, Style 2: 16 digits scientific
		if style == 1 {
			return strconv.FormatFloat(v.floatVal, 'e', 8, 64), nil
		}
		if style == 2 {
			return strconv.FormatFloat(v.floatVal, 'e', 16, 64), nil
		}
		return strconv.FormatFloat(v.floatVal, 'g', -1, 64), nil

	case TypeDecimal, TypeNumeric:
		return v.decimalVal.String(), nil

	case TypeMoney, TypeSmallMoney:
		if style == 1 {
			// With commas
			return formatMoneyWithCommas(v.decimalVal), nil
		}
		return v.decimalVal.StringFixed(4), nil

	case TypeDateTime, TypeDateTime2, TypeSmallDateTime:
		return formatDateTimeWithStyle(v.timeVal, style), nil

	case TypeDate:
		return formatDateWithStyle(v.timeVal, style), nil

	case TypeTime:
		return formatTimeWithStyle(v.timeVal, style), nil

	case TypeBinary, TypeVarBinary:
		return fmt.Sprintf("0x%X", v.bytesVal), nil

	case TypeUniqueIdentifier:
		return v.stringVal, nil

	default:
		return v.AsString(), nil
	}
}

// parseDateTimeWithStyle parses a datetime string using SQL Server style codes
func parseDateTimeWithStyle(s string, style int) (time.Time, error) {
	s = strings.TrimSpace(s)

	// Style-specific formats
	formats := map[int]string{
		0:   "Jan  2 2006 3:04PM",        // Default
		1:   "01/02/06",                  // USA mm/dd/yy
		2:   "06.01.02",                  // ANSI yy.mm.dd
		3:   "02/01/06",                  // British/French dd/mm/yy
		4:   "02.01.06",                  // German dd.mm.yy
		5:   "02-01-06",                  // Italian dd-mm-yy
		6:   "02 Jan 06",                 // dd mon yy
		7:   "Jan 02, 06",                // mon dd, yy
		8:   "15:04:05",                  // hh:mm:ss
		9:   "Jan  2 2006 3:04:05.000PM", // Default + milliseconds
		10:  "01-02-06",                  // USA mm-dd-yy
		11:  "06/01/02",                  // Japan yy/mm/dd
		12:  "060102",                    // ISO yymmdd
		13:  "02 Jan 2006 15:04:05.000",  // Europe default + ms
		14:  "15:04:05.000",              // hh:mm:ss.mmm
		20:  "2006-01-02 15:04:05",       // ODBC canonical
		21:  "2006-01-02 15:04:05.000",   // ODBC canonical + ms
		22:  "01/02/06  3:04:05 PM",      // USA mm/dd/yy hh:mm:ss AM/PM
		23:  "2006-01-02",                // ISO8601 date
		24:  "15:04:05",                  // hh:mm:ss
		25:  "2006-01-02 15:04:05.000",   // ODBC canonical + ms
		100: "Jan  2 2006 3:04PM",        // Default
		101: "01/02/2006",                // USA mm/dd/yyyy
		102: "2006.01.02",                // ANSI yyyy.mm.dd
		103: "02/01/2006",                // British/French dd/mm/yyyy
		104: "02.01.2006",                // German dd.mm.yyyy
		105: "02-01-2006",                // Italian dd-mm-yyyy
		106: "02 Jan 2006",               // dd mon yyyy
		107: "Jan 02, 2006",              // mon dd, yyyy
		108: "15:04:05",                  // hh:mm:ss
		109: "Jan  2 2006 3:04:05.000PM", // Default + milliseconds
		110: "01-02-2006",                // USA mm-dd-yyyy
		111: "2006/01/02",                // Japan yyyy/mm/dd
		112: "20060102",                  // ISO yyyymmdd
		113: "02 Jan 2006 15:04:05.000",  // Europe default + ms
		114: "15:04:05.000",              // hh:mm:ss.mmm
		120: "2006-01-02 15:04:05",       // ODBC canonical
		121: "2006-01-02 15:04:05.000",   // ODBC canonical + ms
		126: "2006-01-02T15:04:05.000",   // ISO8601
		127: "2006-01-02T15:04:05.000Z",  // ISO8601 with Z
	}

	// Try the specified style first
	if format, ok := formats[style]; ok {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	// Try common formats
	commonFormats := []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"01/02/2006 15:04:05",
		"01/02/2006",
		"02/01/2006",
		"2006.01.02",
		"Jan 2 2006 3:04PM",
		"Jan 2 2006 3:04:05PM",
		time.RFC3339,
		time.RFC3339Nano,
	}

	for _, format := range commonFormats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse datetime: %s", s)
}

// formatDateTimeWithStyle formats a datetime using SQL Server style codes
func formatDateTimeWithStyle(t time.Time, style int) string {
	formats := map[int]string{
		0:   "Jan  2 2006  3:04PM",
		1:   "01/02/06",
		2:   "06.01.02",
		3:   "02/01/06",
		4:   "02.01.06",
		5:   "02-01-06",
		6:   "02 Jan 06",
		7:   "Jan 02, 06",
		8:   "15:04:05",
		9:   "Jan  2 2006  3:04:05.000PM",
		10:  "01-02-06",
		11:  "06/01/02",
		12:  "060102",
		13:  "02 Jan 2006 15:04:05.000",
		14:  "15:04:05.000",
		20:  "2006-01-02 15:04:05",
		21:  "2006-01-02 15:04:05.000",
		22:  "01/02/06  3:04:05 PM",
		23:  "2006-01-02",
		24:  "15:04:05",
		25:  "2006-01-02 15:04:05.000",
		100: "Jan  2 2006  3:04PM",
		101: "01/02/2006",
		102: "2006.01.02",
		103: "02/01/2006",
		104: "02.01.2006",
		105: "02-01-2006",
		106: "02 Jan 2006",
		107: "Jan 02, 2006",
		108: "15:04:05",
		109: "Jan  2 2006  3:04:05.000PM",
		110: "01-02-2006",
		111: "2006/01/02",
		112: "20060102",
		113: "02 Jan 2006 15:04:05.000",
		114: "15:04:05.000",
		120: "2006-01-02 15:04:05",
		121: "2006-01-02 15:04:05.000",
		126: "2006-01-02T15:04:05.000",
		127: "2006-01-02T15:04:05.000Z",
	}

	if format, ok := formats[style]; ok {
		return t.Format(format)
	}
	return t.Format("2006-01-02 15:04:05")
}

// formatDateWithStyle formats a date using SQL Server style codes
func formatDateWithStyle(t time.Time, style int) string {
	formats := map[int]string{
		0:   "Jan  2 2006",
		1:   "01/02/06",
		2:   "06.01.02",
		3:   "02/01/06",
		4:   "02.01.06",
		5:   "02-01-06",
		6:   "02 Jan 06",
		7:   "Jan 02, 06",
		10:  "01-02-06",
		11:  "06/01/02",
		12:  "060102",
		23:  "2006-01-02",
		100: "Jan  2 2006",
		101: "01/02/2006",
		102: "2006.01.02",
		103: "02/01/2006",
		104: "02.01.2006",
		105: "02-01-2006",
		106: "02 Jan 2006",
		107: "Jan 02, 2006",
		110: "01-02-2006",
		111: "2006/01/02",
		112: "20060102",
	}

	if format, ok := formats[style]; ok {
		return t.Format(format)
	}
	return t.Format("2006-01-02")
}

// formatTimeWithStyle formats a time using SQL Server style codes
func formatTimeWithStyle(t time.Time, style int) string {
	formats := map[int]string{
		0:   "15:04:05",
		8:   "15:04:05",
		14:  "15:04:05.000",
		24:  "15:04:05",
		108: "15:04:05",
		114: "15:04:05.000",
	}

	if format, ok := formats[style]; ok {
		return t.Format(format)
	}
	return t.Format("15:04:05")
}

// formatMoneyWithCommas formats money with thousands separators
func formatMoneyWithCommas(d decimal.Decimal) string {
	s := d.StringFixed(2)
	parts := strings.Split(s, ".")

	// Add commas to integer part
	intPart := parts[0]
	negative := strings.HasPrefix(intPart, "-")
	if negative {
		intPart = intPart[1:]
	}

	n := len(intPart)
	if n <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}

	var result strings.Builder
	for i, c := range intPart {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteRune(',')
		}
		result.WriteRune(c)
	}

	if len(parts) > 1 {
		result.WriteRune('.')
		result.WriteString(parts[1])
	}

	if negative {
		return "-" + result.String()
	}
	return result.String()
}

// ParseDataType parses a T-SQL type name into DataType with precision/scale/maxlen
func ParseDataType(typeName string) (DataType, int, int, int) {
	typeName = strings.ToLower(strings.TrimSpace(typeName))

	// Extract precision/scale/length from parentheses
	precision, scale, maxLen := 0, 0, 0
	if idx := strings.Index(typeName, "("); idx > 0 {
		spec := typeName[idx+1 : len(typeName)-1]
		typeName = typeName[:idx]

		parts := strings.Split(spec, ",")
		if len(parts) >= 1 {
			if parts[0] == "max" {
				maxLen = -1 // MAX
			} else {
				n, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
				precision = n
				maxLen = n
			}
		}
		if len(parts) >= 2 {
			n, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			scale = n
		}
	}

	types := map[string]DataType{
		"bit":              TypeBit,
		"tinyint":          TypeTinyInt,
		"smallint":         TypeSmallInt,
		"int":              TypeInt,
		"integer":          TypeInt,
		"bigint":           TypeBigInt,
		"decimal":          TypeDecimal,
		"numeric":          TypeNumeric,
		"money":            TypeMoney,
		"smallmoney":       TypeSmallMoney,
		"float":            TypeFloat,
		"real":             TypeReal,
		"date":             TypeDate,
		"time":             TypeTime,
		"datetime":         TypeDateTime,
		"datetime2":        TypeDateTime2,
		"smalldatetime":    TypeSmallDateTime,
		"datetimeoffset":   TypeDateTimeOffset,
		"char":             TypeChar,
		"varchar":          TypeVarChar,
		"nchar":            TypeNChar,
		"nvarchar":         TypeNVarChar,
		"text":             TypeText,
		"ntext":            TypeNText,
		"binary":           TypeBinary,
		"varbinary":        TypeVarBinary,
		"uniqueidentifier": TypeUniqueIdentifier,
		"xml":              TypeXML,
		"table":            TypeTable,
	}

	if dt, ok := types[typeName]; ok {
		return dt, precision, scale, maxLen
	}
	return TypeUnknown, 0, 0, 0
}
