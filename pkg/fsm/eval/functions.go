package eval

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
	"github.com/shopspring/decimal"
)

// maxFunctionOutputBytes bounds the size of a string a single FSM function call
// may allocate. It guards the string-producing functions (REPLICATE, SPACE,
// STR) against unbounded allocation (D-008): their count/width argument is
// user-controlled, and a large value (e.g. REPLICATE('x', 1e12)) produces an
// out-of-memory fatal, which is NOT a panic and CANNOT be caught by recover() —
// so it takes down the whole process rather than failing the single request.
//
// 16 MiB is far above any legitimate guard/set expression output yet small
// enough that a single allocation cannot exhaust the process. A request for
// more returns a clean error instead of allocating.
const maxFunctionOutputBytes = 16 << 20 // 16 MiB

// checkOutputSize returns an error if producing n bytes would exceed the
// per-function output limit. n is the projected output size in bytes.
func checkOutputSize(fn string, n int) error {
	if n < 0 || n > maxFunctionOutputBytes {
		return fmt.Errorf("%s output size %d exceeds limit %d", fn, n, maxFunctionOutputBytes)
	}
	return nil
}

// Function is a T-SQL function implementation
type Function func(args []Value) (Value, error)

// FunctionRegistry holds all registered functions
type FunctionRegistry struct {
	functions map[string]Function
}

// NewFunctionRegistry creates a new function registry with built-in functions
func NewFunctionRegistry() *FunctionRegistry {
	r := &FunctionRegistry{
		functions: make(map[string]Function),
	}
	r.registerBuiltins()
	return r
}

// Register adds a function to the registry
func (r *FunctionRegistry) Register(name string, fn Function) {
	r.functions[strings.ToUpper(name)] = fn
}

// Call invokes a function by name
func (r *FunctionRegistry) Call(name string, args []Value) (Value, error) {
	fn, ok := r.functions[strings.ToUpper(name)]
	if !ok {
		return Value{}, fmt.Errorf("unknown function: %s", name)
	}
	return fn(args)
}

// Has returns true if the function exists
func (r *FunctionRegistry) Has(name string) bool {
	_, ok := r.functions[strings.ToUpper(name)]
	return ok
}

func (r *FunctionRegistry) registerBuiltins() {
	// String functions
	r.Register("LEN", fnLen)
	r.Register("DATALENGTH", fnDataLength)
	r.Register("SUBSTRING", fnSubstring)
	r.Register("LEFT", fnLeft)
	r.Register("RIGHT", fnRight)
	r.Register("LTRIM", fnLTrim)
	r.Register("RTRIM", fnRTrim)
	r.Register("TRIM", fnTrim)
	r.Register("UPPER", fnUpper)
	r.Register("LOWER", fnLower)
	r.Register("REPLACE", fnReplace)
	r.Register("CHARINDEX", fnCharIndex)
	r.Register("PATINDEX", fnPatIndex)
	r.Register("CONCAT", fnConcat)
	r.Register("CONCAT_WS", fnConcatWS)
	r.Register("STUFF", fnStuff)
	r.Register("REVERSE", fnReverse)
	r.Register("REPLICATE", fnReplicate)
	r.Register("SPACE", fnSpace)
	r.Register("STR", fnStr)
	r.Register("CHAR", fnChar)
	r.Register("ASCII", fnAscii)
	r.Register("UNICODE", fnUnicode)
	r.Register("NCHAR", fnNChar)
	r.Register("QUOTENAME", fnQuoteName)
	r.Register("FORMAT", fnFormat)

	// NULL handling functions
	r.Register("ISNULL", fnIsNull)
	r.Register("COALESCE", fnCoalesce)
	r.Register("NULLIF", fnNullIf)
	r.Register("IIF", fnIIF)
	r.Register("CHOOSE", fnChoose)

	// Date/time functions
	r.Register("GETDATE", fnGetDate)
	r.Register("GETUTCDATE", fnGetUTCDate)
	r.Register("SYSDATETIME", fnSysDateTime)
	r.Register("SYSUTCDATETIME", fnSysUTCDateTime)
	r.Register("CURRENT_TIMESTAMP", fnGetDate)
	r.Register("DATEADD", fnDateAdd)
	r.Register("DATEDIFF", fnDateDiff)
	r.Register("DATEDIFF_BIG", fnDateDiffBig)
	r.Register("DATEPART", fnDatePart)
	r.Register("DATENAME", fnDateName)
	r.Register("DAY", fnDay)
	r.Register("MONTH", fnMonth)
	r.Register("YEAR", fnYear)
	r.Register("EOMONTH", fnEOMonth)
	r.Register("DATEFROMPARTS", fnDateFromParts)
	r.Register("ISDATE", fnIsDate)

	// Numeric functions
	r.Register("ABS", fnAbs)
	r.Register("CEILING", fnCeiling)
	r.Register("FLOOR", fnFloor)
	r.Register("ROUND", fnRound)
	r.Register("SIGN", fnSign)
	r.Register("POWER", fnPower)
	r.Register("SQRT", fnSqrt)
	r.Register("SQUARE", fnSquare)
	r.Register("EXP", fnExp)
	r.Register("LOG", fnLog)
	r.Register("LOG10", fnLog10)
	r.Register("PI", fnPi)
	r.Register("RAND", fnRand)

	// Type checking functions
	r.Register("ISNUMERIC", fnIsNumeric)

	// System functions
	r.Register("NEWID", fnNewID)
	r.Register("OBJECT_ID", fnObjectID)
	r.Register("OBJECT_NAME", fnObjectName)
	r.Register("DB_ID", fnDBID)
	r.Register("DB_NAME", fnDBName)
	r.Register("SCHEMA_ID", fnSchemaID)
	r.Register("SCHEMA_NAME", fnSchemaName)
	r.Register("SCOPE_IDENTITY", fnScopeIdentity)
	r.Register("IDENT_CURRENT", fnIdentCurrent)
	r.Register("@@IDENTITY", fnIdentity)
	r.Register("@@ROWCOUNT", fnRowCount)
	r.Register("@@ERROR", fnError)
	r.Register("@@TRANCOUNT", fnTranCount)

	// Error functions (Stage 2)
	r.Register("ERROR_NUMBER", fnErrorNumber)
	r.Register("ERROR_MESSAGE", fnErrorMessage)
	r.Register("ERROR_LINE", fnErrorLine)
	r.Register("ERROR_PROCEDURE", fnErrorProcedure)
	r.Register("ERROR_STATE", fnErrorState)
	r.Register("ERROR_SEVERITY", fnErrorSeverity)
	r.Register("XACT_STATE", fnXactState)

	// Additional math functions
	r.Register("SIN", fnSin)
	r.Register("COS", fnCos)
	r.Register("TAN", fnTan)
	r.Register("ASIN", fnASin)
	r.Register("ACOS", fnACos)
	r.Register("ATAN", fnATan)
	r.Register("ATN2", fnATan2)
	r.Register("COT", fnCot)
	r.Register("DEGREES", fnDegrees)
	r.Register("RADIANS", fnRadians)

	// Additional string functions
	r.Register("STRING_AGG", fnStringAgg)
	r.Register("STRING_SPLIT", fnStringSplit)
	r.Register("TRANSLATE", fnTranslate)
	r.Register("DIFFERENCE", fnDifference)
	r.Register("SOUNDEX", fnSoundex)

	// Additional date functions
	r.Register("TIMEFROMPARTS", fnTimeFromParts)
	r.Register("DATETIMEFROMPARTS", fnDateTimeFromParts)
	r.Register("DATETIME2FROMPARTS", fnDateTime2FromParts)
	r.Register("SWITCHOFFSET", fnSwitchOffset)
	r.Register("TODATETIMEOFFSET", fnToDateTimeOffset)

	// SQL Server metadata functions
	r.Register("SERVERPROPERTY", fnServerProperty)
	r.Register("COLUMNPROPERTY", fnColumnProperty)
	r.Register("OBJECTPROPERTY", fnObjectProperty)
	r.Register("OBJECTPROPERTYEX", fnObjectProperty)
	r.Register("DATABASEPROPERTYEX", fnDatabasePropertyEx)
	r.Register("TYPEPROPERTY", fnTypeProperty)
	r.Register("INDEXPROPERTY", fnIndexProperty)
	r.Register("HAS_PERMS_BY_NAME", fnHasPermsById)

	// User/login functions
	r.Register("SUSER_SNAME", fnSUserSName)
	r.Register("SUSER_NAME", fnSUserName)
	r.Register("SYSTEM_USER", fnSystemUser)
	r.Register("USER_NAME", fnUserName)
	r.Register("CURRENT_USER", fnCurrentUser)
	r.Register("SESSION_USER", fnSessionUser)
	r.Register("ORIGINAL_LOGIN", fnOriginalLogin)
	r.Register("APP_NAME", fnAppName)
	r.Register("HOST_NAME", fnHostName)
}

// ============ String functions ============

func fnLen(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("LEN requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	// LEN returns character count, not byte count
	s := args[0].AsString()
	return NewInt(int64(utf8.RuneCountInString(s))), nil
}

func fnDataLength(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("DATALENGTH requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	// DATALENGTH returns byte count
	s := args[0].AsString()
	return NewInt(int64(len(s))), nil
}

func fnSubstring(args []Value) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("SUBSTRING requires 2 or 3 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	s := []rune(args[0].AsString())
	start := int(args[1].AsInt()) - 1 // SQL is 1-based
	if start < 0 {
		start = 0
	}
	if start >= len(s) {
		return NewVarChar("", -1), nil
	}

	length := len(s) - start
	if len(args) >= 3 && !args[2].IsNull {
		length = int(args[2].AsInt())
	}

	end := start + length
	if end > len(s) {
		end = len(s)
	}
	// D-008: a negative length makes end < start, which panics in s[start:end].
	// T-SQL treats a negative SUBSTRING length as an error / empty selection;
	// clamp to an empty result rather than slicing backwards.
	if end < start {
		end = start
	}

	return NewVarChar(string(s[start:end]), -1), nil
}

func fnLeft(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("LEFT requires 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	s := []rune(args[0].AsString())
	n := int(args[1].AsInt())
	if n < 0 {
		n = 0
	}
	if n > len(s) {
		n = len(s)
	}
	return NewVarChar(string(s[:n]), -1), nil
}

func fnRight(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("RIGHT requires 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	s := []rune(args[0].AsString())
	n := int(args[1].AsInt())
	if n < 0 {
		n = 0
	}
	if n > len(s) {
		n = len(s)
	}
	return NewVarChar(string(s[len(s)-n:]), -1), nil
}

func fnLTrim(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("LTRIM requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(strings.TrimLeft(args[0].AsString(), " \t"), -1), nil
}

func fnRTrim(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("RTRIM requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(strings.TrimRight(args[0].AsString(), " \t"), -1), nil
}

func fnTrim(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("TRIM requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(strings.TrimSpace(args[0].AsString()), -1), nil
}

func fnUpper(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("UPPER requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(strings.ToUpper(args[0].AsString()), -1), nil
}

func fnLower(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("LOWER requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(strings.ToLower(args[0].AsString()), -1), nil
}

func fnReplace(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("REPLACE requires 3 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	s := args[0].AsString()
	old := args[1].AsString()
	new := args[2].AsString()
	return NewVarChar(strings.ReplaceAll(s, old, new), -1), nil
}

func fnCharIndex(args []Value) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("CHARINDEX requires 2 or 3 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return Null(TypeInt), nil
	}

	substr := args[0].AsString()
	str := args[1].AsString()
	start := 0
	if len(args) >= 3 && !args[2].IsNull {
		start = int(args[2].AsInt()) - 1 // 1-based
		if start < 0 {
			start = 0
		}
	}

	if start >= len(str) {
		return NewInt(0), nil
	}

	idx := strings.Index(str[start:], substr)
	if idx < 0 {
		return NewInt(0), nil
	}
	return NewInt(int64(idx + start + 1)), nil // 1-based
}

func fnPatIndex(args []Value) (Value, error) {
	// Simplified PATINDEX - only handles basic patterns
	if len(args) != 2 {
		return Value{}, fmt.Errorf("PATINDEX requires 2 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return Null(TypeInt), nil
	}

	pattern := args[0].AsString()
	str := args[1].AsString()

	// Remove % from pattern for simple matching
	pattern = strings.Trim(pattern, "%")
	idx := strings.Index(str, pattern)
	if idx < 0 {
		return NewInt(0), nil
	}
	return NewInt(int64(idx + 1)), nil // 1-based
}

func fnConcat(args []Value) (Value, error) {
	var result strings.Builder
	for _, arg := range args {
		if !arg.IsNull {
			result.WriteString(arg.AsString())
		}
	}
	return NewVarChar(result.String(), -1), nil
}

func fnConcatWS(args []Value) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("CONCAT_WS requires at least 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	separator := args[0].AsString()
	var parts []string
	for _, arg := range args[1:] {
		if !arg.IsNull {
			parts = append(parts, arg.AsString())
		}
	}
	return NewVarChar(strings.Join(parts, separator), -1), nil
}

func fnStuff(args []Value) (Value, error) {
	if len(args) != 4 {
		return Value{}, fmt.Errorf("STUFF requires 4 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	s := []rune(args[0].AsString())
	start := int(args[1].AsInt()) - 1 // 1-based
	length := int(args[2].AsInt())
	insert := args[3].AsString()

	if start < 0 || start > len(s) {
		return Null(TypeVarChar), nil
	}

	end := start + length
	if end > len(s) {
		end = len(s)
	}
	// D-008: a negative length makes end < start (and possibly < 0), which
	// panics in s[end:]. Clamp so the removed span is empty rather than slicing
	// backwards; STUFF with a non-positive length removes nothing.
	if end < start {
		end = start
	}

	result := string(s[:start]) + insert + string(s[end:])
	return NewVarChar(result, -1), nil
}

func fnReverse(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("REVERSE requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	runes := []rune(args[0].AsString())
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return NewVarChar(string(runes), -1), nil
}

func fnReplicate(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("REPLICATE requires 2 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return Null(TypeVarChar), nil
	}

	s := args[0].AsString()
	n := int(args[1].AsInt())
	if n < 0 {
		return Null(TypeVarChar), nil
	}
	// D-008: bound the projected output before allocating. strings.Repeat with a
	// large n is an unrecoverable OOM, not a catchable panic.
	if err := checkOutputSize("REPLICATE", len(s)*n); err != nil {
		return Value{}, err
	}
	return NewVarChar(strings.Repeat(s, n), -1), nil
}

func fnSpace(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("SPACE requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	n := int(args[0].AsInt())
	if n < 0 {
		return NewVarChar("", -1), nil
	}
	// D-008: bound the projected output before allocating.
	if err := checkOutputSize("SPACE", n); err != nil {
		return Value{}, err
	}
	return NewVarChar(strings.Repeat(" ", n), -1), nil
}

func fnStr(args []Value) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("STR requires 1 to 3 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	f := args[0].AsFloat()
	length := 10
	decimals := 0

	if len(args) >= 2 && !args[1].IsNull {
		length = int(args[1].AsInt())
	}
	if len(args) >= 3 && !args[2].IsNull {
		decimals = int(args[2].AsInt())
	}
	// D-008: the length argument becomes a fmt field width; a huge width pads to
	// a correspondingly huge string. Bound it before formatting. A negative
	// width left-justifies in fmt; its magnitude is what drives allocation.
	width := length
	if width < 0 {
		width = -width
	}
	if err := checkOutputSize("STR", width); err != nil {
		return Value{}, err
	}

	format := fmt.Sprintf("%%%d.%df", length, decimals)
	s := fmt.Sprintf(format, f)
	return NewVarChar(s, -1), nil
}

func fnChar(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("CHAR requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeChar), nil
	}
	n := int(args[0].AsInt())
	if n < 0 || n > 255 {
		return Null(TypeChar), nil
	}
	return NewChar(string(rune(n)), 1), nil
}

func fnAscii(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ASCII requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	s := args[0].AsString()
	if len(s) == 0 {
		return Null(TypeInt), nil
	}
	return NewInt(int64(s[0])), nil
}

func fnUnicode(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("UNICODE requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	s := args[0].AsString()
	if len(s) == 0 {
		return Null(TypeInt), nil
	}
	r, _ := utf8.DecodeRuneInString(s)
	return NewInt(int64(r)), nil
}

func fnNChar(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("NCHAR requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeNChar), nil
	}
	n := int(args[0].AsInt())
	return NewNVarChar(string(rune(n)), 1), nil
}

func fnQuoteName(args []Value) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("QUOTENAME requires 1 or 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	s := args[0].AsString()
	quote := "["
	closeQuote := "]"

	if len(args) >= 2 && !args[1].IsNull {
		q := args[1].AsString()
		if len(q) > 0 {
			quote = string(q[0])
			switch quote {
			case "[":
				closeQuote = "]"
			case "'", "\"":
				closeQuote = quote
			default:
				closeQuote = quote
			}
		}
	}

	// Escape the close quote within the string
	s = strings.ReplaceAll(s, closeQuote, closeQuote+closeQuote)
	return NewVarChar(quote+s+closeQuote, -1), nil
}

func fnFormat(args []Value) (Value, error) {
	// Simplified FORMAT - basic support
	if len(args) < 2 {
		return Value{}, fmt.Errorf("FORMAT requires at least 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	format := args[1].AsString()

	// Handle datetime formatting
	if args[0].Type.IsDateTime() {
		t := args[0].AsTime()
		// Convert .NET-style format to Go format
		goFormat := convertDotNetToGoFormat(format)
		return NewVarChar(t.Format(goFormat), -1), nil
	}

	// Handle numeric formatting - simplified
	return NewVarChar(args[0].AsString(), -1), nil
}

func convertDotNetToGoFormat(format string) string {
	replacements := map[string]string{
		"yyyy": "2006",
		"yy":   "06",
		"MMMM": "January",
		"MMM":  "Jan",
		"MM":   "01",
		"M":    "1",
		"dddd": "Monday",
		"ddd":  "Mon",
		"dd":   "02",
		"d":    "2",
		"HH":   "15",
		"hh":   "03",
		"h":    "3",
		"mm":   "04",
		"ss":   "05",
		"tt":   "PM",
		"fff":  "000",
		"ff":   "00",
		"f":    "0",
	}

	result := format
	for dotNet, golang := range replacements {
		result = strings.ReplaceAll(result, dotNet, golang)
	}
	return result
}

// ============ NULL handling functions ============

func fnIsNull(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("ISNULL requires 2 arguments")
	}
	if args[0].IsNull {
		return args[1], nil
	}
	return args[0], nil
}

func fnCoalesce(args []Value) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("COALESCE requires at least 1 argument")
	}
	for _, arg := range args {
		if !arg.IsNull {
			return arg, nil
		}
	}
	return Null(args[0].Type), nil
}

func fnNullIf(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("NULLIF requires 2 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return args[0], nil
	}
	if args[0].Compare(args[1]) == 0 {
		return Null(args[0].Type), nil
	}
	return args[0], nil
}

func fnIIF(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("IIF requires 3 arguments")
	}
	if args[0].IsNull {
		return args[2], nil
	}
	if args[0].AsBool() {
		return args[1], nil
	}
	return args[2], nil
}

func fnChoose(args []Value) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("CHOOSE requires at least 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	index := int(args[0].AsInt())
	if index < 1 || index >= len(args) {
		return Null(TypeVarChar), nil
	}
	return args[index], nil
}

// ============ Date/time functions ============

func fnGetDate(args []Value) (Value, error) {
	// GETDATE() returns LOCAL server time by T-SQL contract — deliberately not
	// routed through xolutime, which has no local-now. Do not "normalise" to UTC:
	// that would break T-SQL emulation. GETUTCDATE() is the UTC counterpart below.
	return NewDateTime(time.Now()), nil
}

func fnGetUTCDate(args []Value) (Value, error) {
	return NewDateTime(ot.Now().Time()), nil
}

func fnSysDateTime(args []Value) (Value, error) {
	// SYSDATETIME() returns LOCAL server time by T-SQL contract (see fnGetDate).
	return NewDateTime(time.Now()), nil
}

func fnSysUTCDateTime(args []Value) (Value, error) {
	return NewDateTime(ot.Now().Time()), nil
}

func fnDateAdd(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("DATEADD requires 3 arguments")
	}
	if args[1].IsNull || args[2].IsNull {
		return Null(TypeDateTime), nil
	}

	interval := strings.ToLower(args[0].AsString())
	number := int(args[1].AsInt())
	date := args[2].AsTime()

	switch interval {
	case "year", "yy", "yyyy":
		return NewDateTime(date.AddDate(number, 0, 0)), nil
	case "quarter", "qq", "q":
		return NewDateTime(date.AddDate(0, number*3, 0)), nil
	case "month", "mm", "m":
		return NewDateTime(date.AddDate(0, number, 0)), nil
	case "dayofyear", "dy", "y", "day", "dd", "d":
		return NewDateTime(date.AddDate(0, 0, number)), nil
	case "week", "wk", "ww":
		return NewDateTime(date.AddDate(0, 0, number*7)), nil
	case "hour", "hh":
		return NewDateTime(date.Add(time.Duration(number) * time.Hour)), nil
	case "minute", "mi", "n":
		return NewDateTime(date.Add(time.Duration(number) * time.Minute)), nil
	case "second", "ss", "s":
		return NewDateTime(date.Add(time.Duration(number) * time.Second)), nil
	case "millisecond", "ms":
		return NewDateTime(date.Add(time.Duration(number) * time.Millisecond)), nil
	case "microsecond", "mcs":
		return NewDateTime(date.Add(time.Duration(number) * time.Microsecond)), nil
	case "nanosecond", "ns":
		return NewDateTime(date.Add(time.Duration(number) * time.Nanosecond)), nil
	default:
		return Value{}, fmt.Errorf("unknown datepart: %s", interval)
	}
}

func fnDateDiff(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("DATEDIFF requires 3 arguments")
	}
	if args[1].IsNull || args[2].IsNull {
		return Null(TypeInt), nil
	}

	interval := strings.ToLower(args[0].AsString())
	startDate := args[1].AsTime()
	endDate := args[2].AsTime()

	diff := endDate.Sub(startDate)

	switch interval {
	case "year", "yy", "yyyy":
		return NewInt(int64(endDate.Year() - startDate.Year())), nil
	case "quarter", "qq", "q":
		startQ := (startDate.Year()*12 + int(startDate.Month()) - 1) / 3
		endQ := (endDate.Year()*12 + int(endDate.Month()) - 1) / 3
		return NewInt(int64(endQ - startQ)), nil
	case "month", "mm", "m":
		return NewInt(int64((endDate.Year()-startDate.Year())*12 + int(endDate.Month()-startDate.Month()))), nil
	case "dayofyear", "dy", "y", "day", "dd", "d":
		return NewInt(int64(diff.Hours() / 24)), nil
	case "week", "wk", "ww":
		return NewInt(int64(diff.Hours() / 24 / 7)), nil
	case "hour", "hh":
		return NewInt(int64(diff.Hours())), nil
	case "minute", "mi", "n":
		return NewInt(int64(diff.Minutes())), nil
	case "second", "ss", "s":
		return NewInt(int64(diff.Seconds())), nil
	case "millisecond", "ms":
		return NewInt(diff.Milliseconds()), nil
	case "microsecond", "mcs":
		return NewInt(diff.Microseconds()), nil
	case "nanosecond", "ns":
		return NewInt(diff.Nanoseconds()), nil
	default:
		return Value{}, fmt.Errorf("unknown datepart: %s", interval)
	}
}

func fnDateDiffBig(args []Value) (Value, error) {
	return fnDateDiff(args) // Same as DATEDIFF but returns bigint
}

func fnDatePart(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("DATEPART requires 2 arguments")
	}
	if args[1].IsNull {
		return Null(TypeInt), nil
	}

	interval := strings.ToLower(args[0].AsString())
	date := args[1].AsTime()

	switch interval {
	case "year", "yy", "yyyy":
		return NewInt(int64(date.Year())), nil
	case "quarter", "qq", "q":
		return NewInt(int64((date.Month()-1)/3 + 1)), nil
	case "month", "mm", "m":
		return NewInt(int64(date.Month())), nil
	case "dayofyear", "dy", "y":
		return NewInt(int64(date.YearDay())), nil
	case "day", "dd", "d":
		return NewInt(int64(date.Day())), nil
	case "week", "wk", "ww":
		_, week := date.ISOWeek()
		return NewInt(int64(week)), nil
	case "weekday", "dw":
		return NewInt(int64(date.Weekday()) + 1), nil // SQL Server is 1-based
	case "hour", "hh":
		return NewInt(int64(date.Hour())), nil
	case "minute", "mi", "n":
		return NewInt(int64(date.Minute())), nil
	case "second", "ss", "s":
		return NewInt(int64(date.Second())), nil
	case "millisecond", "ms":
		return NewInt(int64(date.Nanosecond() / 1000000)), nil
	case "microsecond", "mcs":
		return NewInt(int64(date.Nanosecond() / 1000)), nil
	case "nanosecond", "ns":
		return NewInt(int64(date.Nanosecond())), nil
	default:
		return Value{}, fmt.Errorf("unknown datepart: %s", interval)
	}
}

func fnDateName(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("DATENAME requires 2 arguments")
	}
	if args[1].IsNull {
		return Null(TypeVarChar), nil
	}

	interval := strings.ToLower(args[0].AsString())
	date := args[1].AsTime()

	switch interval {
	case "year", "yy", "yyyy":
		return NewVarChar(fmt.Sprintf("%d", date.Year()), -1), nil
	case "month", "mm", "m":
		return NewVarChar(date.Month().String(), -1), nil
	case "weekday", "dw":
		return NewVarChar(date.Weekday().String(), -1), nil
	default:
		// For other parts, return numeric value as string
		result, err := fnDatePart(args)
		if err != nil {
			return Value{}, err
		}
		return NewVarChar(result.AsString(), -1), nil
	}
}

func fnDay(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("DAY requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	return NewInt(int64(args[0].AsTime().Day())), nil
}

func fnMonth(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("MONTH requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	return NewInt(int64(args[0].AsTime().Month())), nil
}

func fnYear(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("YEAR requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	return NewInt(int64(args[0].AsTime().Year())), nil
}

func fnEOMonth(args []Value) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("EOMONTH requires 1 or 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeDate), nil
	}

	date := args[0].AsTime()
	monthsToAdd := 0
	if len(args) >= 2 && !args[1].IsNull {
		monthsToAdd = int(args[1].AsInt())
	}

	// Move to next month, then subtract a day
	date = date.AddDate(0, monthsToAdd+1, 0)
	date = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location())
	date = date.AddDate(0, 0, -1)

	return NewDate(date), nil
}

func fnDateFromParts(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("DATEFROMPARTS requires 3 arguments")
	}
	if args[0].IsNull || args[1].IsNull || args[2].IsNull {
		return Null(TypeDate), nil
	}

	year := int(args[0].AsInt())
	month := int(args[1].AsInt())
	day := int(args[2].AsInt())

	return NewDate(time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)), nil
}

func fnIsDate(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ISDATE requires 1 argument")
	}
	if args[0].IsNull {
		return NewInt(0), nil
	}
	if args[0].Type.IsDateTime() {
		return NewInt(1), nil
	}
	// Try to parse string
	_, err := parseDateTimeWithStyle(args[0].AsString(), 0)
	if err == nil {
		return NewInt(1), nil
	}
	return NewInt(0), nil
}

// ============ Numeric functions ============

func fnAbs(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ABS requires 1 argument")
	}
	if args[0].IsNull {
		return Null(args[0].Type), nil
	}

	switch args[0].Type {
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return NewDecimal(args[0].decimalVal.Abs(), args[0].Precision, args[0].Scale), nil
	case TypeFloat, TypeReal:
		return NewFloat(math.Abs(args[0].floatVal)), nil
	default:
		v := args[0].intVal
		if v < 0 {
			v = -v
		}
		return NewBigInt(v), nil
	}
}

func fnCeiling(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("CEILING requires 1 argument")
	}
	if args[0].IsNull {
		return Null(args[0].Type), nil
	}

	switch args[0].Type {
	case TypeDecimal, TypeNumeric:
		return NewDecimal(args[0].decimalVal.Ceil(), args[0].Precision, 0), nil
	case TypeFloat, TypeReal:
		return NewFloat(math.Ceil(args[0].floatVal)), nil
	default:
		return args[0], nil // Integer unchanged
	}
}

func fnFloor(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("FLOOR requires 1 argument")
	}
	if args[0].IsNull {
		return Null(args[0].Type), nil
	}

	switch args[0].Type {
	case TypeDecimal, TypeNumeric:
		return NewDecimal(args[0].decimalVal.Floor(), args[0].Precision, 0), nil
	case TypeFloat, TypeReal:
		return NewFloat(math.Floor(args[0].floatVal)), nil
	default:
		return args[0], nil
	}
}

func fnRound(args []Value) (Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return Value{}, fmt.Errorf("ROUND requires 2 or 3 arguments")
	}
	if args[0].IsNull {
		return Null(args[0].Type), nil
	}

	decimals := int(args[1].AsInt())
	truncate := len(args) >= 3 && !args[2].IsNull && args[2].AsInt() != 0

	switch args[0].Type {
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		d := args[0].decimalVal
		if truncate {
			d = d.Truncate(int32(decimals))
		} else {
			d = d.Round(int32(decimals))
		}
		return NewDecimal(d, args[0].Precision, decimals), nil
	default:
		f := args[0].AsFloat()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return NewFloat(f), nil
		}
		// math.Pow(10, decimals) overflows to +Inf for decimals beyond ~308.
		// The subsequent Trunc/Round(f*scale)/scale then produces Inf/Inf = NaN,
		// silently corrupting the result and panicking any downstream call that
		// converts the value to a decimal. Reject the overflow at the source.
		if decimals > 15 || decimals < -15 {
			return Value{}, fmt.Errorf("ROUND: decimal places %d out of range (must be between -15 and 15)", decimals)
		}
		scale := math.Pow(10, float64(decimals))
		if truncate {
			f = math.Trunc(f*scale) / scale
		} else {
			f = math.Round(f*scale) / scale
		}
		return NewFloat(f), nil
	}
}

func fnSign(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("SIGN requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}

	d := args[0].AsDecimal()
	if d.IsZero() {
		return NewInt(0), nil
	}
	if d.IsNegative() {
		return NewInt(-1), nil
	}
	return NewInt(1), nil
}

func fnPower(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("POWER requires 2 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Pow(args[0].AsFloat(), args[1].AsFloat())), nil
}

func fnSqrt(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("SQRT requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	v := args[0].AsFloat()
	if v < 0 {
		return Value{}, fmt.Errorf("cannot take square root of negative number")
	}
	return NewFloat(math.Sqrt(v)), nil
}

func fnSquare(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("SQUARE requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	v := args[0].AsFloat()
	return NewFloat(v * v), nil
}

func fnExp(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("EXP requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Exp(args[0].AsFloat())), nil
}

func fnLog(args []Value) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("LOG requires 1 or 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}

	v := args[0].AsFloat()
	if v <= 0 {
		return Value{}, fmt.Errorf("LOG requires positive value")
	}

	if len(args) == 1 {
		return NewFloat(math.Log(v)), nil
	}

	base := args[1].AsFloat()
	if base <= 0 || base == 1 {
		return Value{}, fmt.Errorf("LOG base must be positive and not 1")
	}
	return NewFloat(math.Log(v) / math.Log(base)), nil
}

func fnLog10(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("LOG10 requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	v := args[0].AsFloat()
	if v <= 0 {
		return Value{}, fmt.Errorf("LOG10 requires positive value")
	}
	return NewFloat(math.Log10(v)), nil
}

func fnPi(args []Value) (Value, error) {
	return NewFloat(math.Pi), nil
}

func fnRand(args []Value) (Value, error) {
	// Note: This uses Go's default random, not seeded
	// For reproducible results, would need to seed
	return NewFloat(float64(time.Now().UnixNano()%1000000) / 1000000), nil
}

// ============ Type checking functions ============

func fnIsNumeric(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ISNUMERIC requires 1 argument")
	}
	if args[0].IsNull {
		return NewInt(0), nil
	}
	if args[0].Type.IsNumeric() {
		return NewInt(1), nil
	}
	// Try to parse string
	s := strings.TrimSpace(args[0].AsString())
	_, err := decimal.NewFromString(s)
	if err == nil {
		return NewInt(1), nil
	}
	return NewInt(0), nil
}

// ============ System functions ============

func fnNewID(args []Value) (Value, error) {
	// NEWID() returns a random (version 4) UUID, matching T-SQL semantics: a
	// unique, unpredictable uniqueidentifier. This shares the same generator as
	// UUID_V4() (uuid.NewRandom). The earlier implementation synthesised a
	// UUID-shaped string from a single nanosecond timestamp, which collided
	// under rapid generation, had a constant final segment (an undefined 64-bit
	// shift), and set no version/variant bits (D-001).
	id, err := uuid.NewRandom()
	if err != nil {
		return Null(TypeVarChar), err
	}
	return NewVarChar(id.String(), 36), nil
}

func fnObjectID(args []Value) (Value, error) {
	// Returns a hash-based object ID that matches sys.* catalog views
	if len(args) < 1 {
		return Value{}, fmt.Errorf("OBJECT_ID requires at least 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}

	// Get the object name and strip database/schema prefixes
	name := args[0].AsString()
	parts := strings.Split(name, ".")
	tableName := parts[len(parts)-1]
	// Remove brackets if present
	tableName = strings.Trim(tableName, "[]")

	// Hash the table name only (must match objectIDForName in syscatalog.go)
	hash := int64(0)
	for _, c := range tableName {
		hash = hash*31 + int64(c)
	}
	return NewInt(hash & 0x7FFFFFFF), nil
}

func fnObjectName(args []Value) (Value, error) {
	// Returns NULL - real implementation requires database metadata
	if len(args) < 1 {
		return Value{}, fmt.Errorf("OBJECT_NAME requires at least 1 argument")
	}
	return Null(TypeVarChar), nil
}

func fnDBID(args []Value) (Value, error) {
	// Returns 1 as placeholder for current database
	if len(args) == 0 {
		return NewInt(1), nil
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	return NewInt(1), nil
}

func fnDBName(args []Value) (Value, error) {
	// Returns placeholder database name
	if len(args) == 0 {
		return NewVarChar("master", -1), nil
	}
	return NewVarChar("master", -1), nil
}

func fnSchemaID(args []Value) (Value, error) {
	if len(args) == 0 {
		return NewInt(1), nil // dbo schema
	}
	if args[0].IsNull {
		return Null(TypeInt), nil
	}
	name := strings.ToLower(args[0].AsString())
	if name == "dbo" {
		return NewInt(1), nil
	}
	return Null(TypeInt), nil
}

func fnSchemaName(args []Value) (Value, error) {
	if len(args) == 0 {
		return NewVarChar("dbo", -1), nil
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	if args[0].AsInt() == 1 {
		return NewVarChar("dbo", -1), nil
	}
	return Null(TypeVarChar), nil
}

// These functions need access to execution context - placeholder implementations
var lastIdentity int64 = 0
var lastRowCount int64 = 0
var lastError int = 0
var tranCount int = 0

func fnScopeIdentity(args []Value) (Value, error) {
	return NewBigInt(lastIdentity), nil
}

func fnIdentCurrent(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("IDENT_CURRENT requires 1 argument")
	}
	return NewBigInt(lastIdentity), nil
}

func fnIdentity(args []Value) (Value, error) {
	return NewBigInt(lastIdentity), nil
}

func fnRowCount(args []Value) (Value, error) {
	return NewInt(lastRowCount), nil
}

func fnError(args []Value) (Value, error) {
	return NewInt(int64(lastError)), nil
}

func fnTranCount(args []Value) (Value, error) {
	return NewInt(int64(tranCount)), nil
}

// ============ Error functions (Stage 2) ============

// These are placeholder implementations - actual values come from ErrorContext
var currentErrorNumber int = 0
var currentErrorMessage string = ""
var currentErrorLine int = 0
var currentErrorProcedure string = ""
var currentErrorState int = 0
var currentErrorSeverity int = 0
var currentXactState int = 0

func fnErrorNumber(args []Value) (Value, error) {
	if currentErrorNumber == 0 {
		return Null(TypeInt), nil
	}
	return NewInt(int64(currentErrorNumber)), nil
}

func fnErrorMessage(args []Value) (Value, error) {
	if currentErrorMessage == "" {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(currentErrorMessage, -1), nil
}

func fnErrorLine(args []Value) (Value, error) {
	if currentErrorLine == 0 {
		return Null(TypeInt), nil
	}
	return NewInt(int64(currentErrorLine)), nil
}

func fnErrorProcedure(args []Value) (Value, error) {
	if currentErrorProcedure == "" {
		return Null(TypeVarChar), nil
	}
	return NewVarChar(currentErrorProcedure, -1), nil
}

func fnErrorState(args []Value) (Value, error) {
	if currentErrorState == 0 {
		return Null(TypeInt), nil
	}
	return NewInt(int64(currentErrorState)), nil
}

func fnErrorSeverity(args []Value) (Value, error) {
	if currentErrorSeverity == 0 {
		return Null(TypeInt), nil
	}
	return NewInt(int64(currentErrorSeverity)), nil
}

func fnXactState(args []Value) (Value, error) {
	return NewInt(int64(currentXactState)), nil
}

// SetErrorContext updates the error context for error functions
func SetErrorContext(errNum int, msg string, line int, proc string, state int, severity int) {
	currentErrorNumber = errNum
	currentErrorMessage = msg
	currentErrorLine = line
	currentErrorProcedure = proc
	currentErrorState = state
	currentErrorSeverity = severity
}

// ClearErrorContext clears the error context
func ClearErrorContext() {
	currentErrorNumber = 0
	currentErrorMessage = ""
	currentErrorLine = 0
	currentErrorProcedure = ""
	currentErrorState = 0
	currentErrorSeverity = 0
}

// ============ Additional Math functions ============

func fnSin(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("SIN requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Sin(args[0].AsFloat())), nil
}

func fnCos(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("COS requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Cos(args[0].AsFloat())), nil
}

func fnTan(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("TAN requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Tan(args[0].AsFloat())), nil
}

func fnASin(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ASIN requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Asin(args[0].AsFloat())), nil
}

func fnACos(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ACOS requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Acos(args[0].AsFloat())), nil
}

func fnATan(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ATAN requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Atan(args[0].AsFloat())), nil
}

func fnATan2(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("ATN2 requires 2 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(math.Atan2(args[0].AsFloat(), args[1].AsFloat())), nil
}

func fnCot(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("COT requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	tan := math.Tan(args[0].AsFloat())
	if tan == 0 {
		return Value{}, fmt.Errorf("COT: division by zero")
	}
	return NewFloat(1 / tan), nil
}

func fnDegrees(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("DEGREES requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(args[0].AsFloat() * 180 / math.Pi), nil
}

func fnRadians(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("RADIANS requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeFloat), nil
	}
	return NewFloat(args[0].AsFloat() * math.Pi / 180), nil
}

// ============ Additional String functions ============

func fnStringAgg(args []Value) (Value, error) {
	// STRING_AGG is an aggregate function - placeholder for scalar use
	if len(args) < 2 {
		return Value{}, fmt.Errorf("STRING_AGG requires at least 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	return args[0], nil
}

func fnStringSplit(args []Value) (Value, error) {
	// STRING_SPLIT returns a table - placeholder returns first element
	if len(args) < 2 {
		return Value{}, fmt.Errorf("STRING_SPLIT requires 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}
	parts := strings.Split(args[0].AsString(), args[1].AsString())
	if len(parts) > 0 {
		return NewVarChar(parts[0], -1), nil
	}
	return Null(TypeVarChar), nil
}

func fnTranslate(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("TRANSLATE requires 3 arguments")
	}
	if args[0].IsNull || args[1].IsNull || args[2].IsNull {
		return Null(TypeVarChar), nil
	}

	input := args[0].AsString()
	from := args[1].AsString()
	to := args[2].AsString()

	if len(from) != len(to) {
		return Value{}, fmt.Errorf("TRANSLATE: second and third arguments must have same length")
	}

	result := input
	fromRunes := []rune(from)
	toRunes := []rune(to)
	for i, r := range fromRunes {
		result = strings.ReplaceAll(result, string(r), string(toRunes[i]))
	}

	return NewVarChar(result, -1), nil
}

func fnDifference(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("DIFFERENCE requires 2 arguments")
	}
	if args[0].IsNull || args[1].IsNull {
		return Null(TypeInt), nil
	}

	// Simplified DIFFERENCE - compare SOUNDEX codes
	s1 := soundex(args[0].AsString())
	s2 := soundex(args[1].AsString())

	// Count matching characters
	matches := 0
	for i := 0; i < len(s1) && i < len(s2); i++ {
		if s1[i] == s2[i] {
			matches++
		}
	}

	return NewInt(int64(matches)), nil
}

func fnSoundex(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("SOUNDEX requires 1 argument")
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	return NewVarChar(soundex(args[0].AsString()), 4), nil
}

// soundex implements the SOUNDEX algorithm
func soundex(s string) string {
	if s == "" {
		return "0000"
	}

	s = strings.ToUpper(s)
	result := make([]byte, 4)
	result[0] = s[0]
	resultIdx := 1

	getCode := func(c byte) byte {
		switch c {
		case 'B', 'F', 'P', 'V':
			return '1'
		case 'C', 'G', 'J', 'K', 'Q', 'S', 'X', 'Z':
			return '2'
		case 'D', 'T':
			return '3'
		case 'L':
			return '4'
		case 'M', 'N':
			return '5'
		case 'R':
			return '6'
		default:
			return '0'
		}
	}

	prevCode := getCode(s[0])

	for i := 1; i < len(s) && resultIdx < 4; i++ {
		code := getCode(s[i])
		if code != '0' && code != prevCode {
			result[resultIdx] = code
			resultIdx++
		}
		if code != '0' {
			prevCode = code
		}
	}

	for resultIdx < 4 {
		result[resultIdx] = '0'
		resultIdx++
	}

	return string(result)
}

// ============ Additional Date functions ============

func fnTimeFromParts(args []Value) (Value, error) {
	if len(args) < 4 {
		return Value{}, fmt.Errorf("TIMEFROMPARTS requires at least 4 arguments")
	}
	for _, a := range args[:4] {
		if a.IsNull {
			return Null(TypeTime), nil
		}
	}

	hour := int(args[0].AsInt())
	minute := int(args[1].AsInt())
	second := int(args[2].AsInt())
	fraction := int(args[3].AsInt())

	t := time.Date(1, 1, 1, hour, minute, second, fraction*1000, time.UTC)
	return NewTime(t), nil
}

func fnDateTimeFromParts(args []Value) (Value, error) {
	if len(args) != 7 {
		return Value{}, fmt.Errorf("DATETIMEFROMPARTS requires 7 arguments")
	}
	for _, a := range args {
		if a.IsNull {
			return Null(TypeDateTime), nil
		}
	}

	year := int(args[0].AsInt())
	month := int(args[1].AsInt())
	day := int(args[2].AsInt())
	hour := int(args[3].AsInt())
	minute := int(args[4].AsInt())
	second := int(args[5].AsInt())
	ms := int(args[6].AsInt())

	t := time.Date(year, time.Month(month), day, hour, minute, second, ms*1000000, time.UTC)
	return NewDateTime(t), nil
}

func fnDateTime2FromParts(args []Value) (Value, error) {
	if len(args) < 7 {
		return Value{}, fmt.Errorf("DATETIME2FROMPARTS requires at least 7 arguments")
	}
	for _, a := range args[:7] {
		if a.IsNull {
			return Null(TypeDateTime2), nil
		}
	}

	year := int(args[0].AsInt())
	month := int(args[1].AsInt())
	day := int(args[2].AsInt())
	hour := int(args[3].AsInt())
	minute := int(args[4].AsInt())
	second := int(args[5].AsInt())
	fraction := int(args[6].AsInt())

	t := time.Date(year, time.Month(month), day, hour, minute, second, fraction*100, time.UTC)
	v := NewDateTime(t)
	v.Type = TypeDateTime2
	return v, nil
}

func fnSwitchOffset(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("SWITCHOFFSET requires 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeDateTimeOffset), nil
	}
	// Simplified - just return the input
	return args[0], nil
}

func fnToDateTimeOffset(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("TODATETIMEOFFSET requires 2 arguments")
	}
	if args[0].IsNull {
		return Null(TypeDateTimeOffset), nil
	}
	// Simplified - just return the input as DateTimeOffset
	v := args[0]
	v.Type = TypeDateTimeOffset
	return v, nil
}

// ============ SQL Server Metadata Functions ============

// fnServerProperty returns SQL Server property values.
// Used by clients like DBeaver to detect server capabilities.
func fnServerProperty(args []Value) (Value, error) {
	if len(args) < 1 {
		return Null(TypeVarChar), nil
	}
	if args[0].IsNull {
		return Null(TypeVarChar), nil
	}

	prop := strings.ToUpper(args[0].AsString())

	switch prop {
	case "PRODUCTVERSION":
		return NewVarChar("15.0.4415.2", -1), nil // SQL Server 2019-like version
	case "PRODUCTLEVEL":
		return NewVarChar("RTM", -1), nil
	case "EDITION":
		return NewVarChar("Developer Edition (64-bit)", -1), nil
	case "ENGINEEDITION":
		return NewInt(3), nil // 3 = Enterprise/Developer
	case "SERVERNAME":
		return NewVarChar("aul", -1), nil
	case "INSTANCENAME":
		return Null(TypeVarChar), nil // Default instance
	case "MACHINENAME":
		return NewVarChar("aul-server", -1), nil
	case "ISCLUSTERED":
		return NewInt(0), nil
	case "ISFULLTEXTINSTALLED":
		return NewInt(0), nil
	case "ISINTEGRATEDSECURITYONLY":
		return NewInt(0), nil
	case "COLLATION":
		return NewVarChar("SQL_Latin1_General_CP1_CI_AS", -1), nil
	case "SQLCHARSETNAME":
		return NewVarChar("iso_1", -1), nil
	case "SQLSORTORDERNAME":
		return NewVarChar("nocase_iso", -1), nil
	case "PROCESSID":
		return NewInt(1), nil
	case "LCID":
		return NewInt(1033), nil // US English
	case "COMPARISONTYPE":
		return NewInt(0), nil
	case "BUILDCLRVERSION":
		return Null(TypeVarChar), nil
	case "RESOURCEVERSION":
		return NewVarChar("15.0.4415.2", -1), nil
	case "RESOURCELASTUPDATEDATETIME":
		return NewDateTime(ot.Now().Time()), nil
	case "HADRMANAGERSTATUS":
		return NewInt(0), nil
	case "ISXTSUPPORTED":
		return NewInt(0), nil
	case "ISPOLYBASEINSTALLED":
		return NewInt(0), nil
	case "ISADVANCEDANALYTICSINSTALLED":
		return NewInt(0), nil
	case "ISTEMPDBMETADATAMEMORYOPTIMIZED":
		return NewInt(0), nil
	default:
		return Null(TypeVarChar), nil
	}
}

// fnColumnProperty returns column property information.
func fnColumnProperty(args []Value) (Value, error) {
	if len(args) < 3 {
		return Null(TypeInt), nil
	}

	// args: object_id, column_name, property_name
	if args[0].IsNull || args[1].IsNull || args[2].IsNull {
		return Null(TypeInt), nil
	}

	prop := strings.ToUpper(args[2].AsString())

	switch prop {
	case "ALLOWSNULL":
		return NewInt(1), nil // Allow NULL by default
	case "ISIDENTITY":
		return NewInt(0), nil
	case "ISCOMPUTED":
		return NewInt(0), nil
	case "ISROWGUIDCOL":
		return NewInt(0), nil
	case "PRECISION":
		return NewInt(0), nil
	case "SCALE":
		return NewInt(0), nil
	case "COLUMNID":
		return NewInt(1), nil
	case "ISSPARSE":
		return NewInt(0), nil
	case "ISCOLUMNSET":
		return NewInt(0), nil
	case "GENERATEDALWAYSTYPE":
		return NewInt(0), nil
	default:
		return Null(TypeInt), nil
	}
}

// fnObjectProperty returns object property information.
func fnObjectProperty(args []Value) (Value, error) {
	if len(args) < 2 {
		return Null(TypeInt), nil
	}

	if args[0].IsNull || args[1].IsNull {
		return Null(TypeInt), nil
	}

	prop := strings.ToUpper(args[1].AsString())

	switch prop {
	case "ISTABLE":
		return NewInt(1), nil
	case "ISVIEW":
		return NewInt(0), nil
	case "ISUSERTABLE":
		return NewInt(1), nil
	case "ISSYSTEMTABLE":
		return NewInt(0), nil
	case "ISMSSHIPPED":
		return NewInt(0), nil
	case "TABLEHASIDENTITY":
		return NewInt(0), nil
	case "TABLEHASINDEX":
		return NewInt(0), nil
	case "TABLEHASPRIMARYKEY":
		return NewInt(0), nil
	case "TABLEHASFOREIGNKEY":
		return NewInt(0), nil
	case "TABLEHASUNIQUECNST":
		return NewInt(0), nil
	case "TABLEHASCHECKCNST":
		return NewInt(0), nil
	case "TABLEHASDEFAULT":
		return NewInt(0), nil
	case "TABLEHASTRIGGER":
		return NewInt(0), nil
	case "TABLEHASCLUSTERINDEX":
		return NewInt(0), nil
	case "TABLEHASNONCLUSTINDEX":
		return NewInt(0), nil
	default:
		return Null(TypeInt), nil
	}
}

// fnDatabasePropertyEx returns database property information.
func fnDatabasePropertyEx(args []Value) (Value, error) {
	if len(args) < 2 {
		return Null(TypeVarChar), nil
	}

	if args[1].IsNull {
		return Null(TypeVarChar), nil
	}

	prop := strings.ToUpper(args[1].AsString())

	switch prop {
	case "COLLATION":
		return NewVarChar("SQL_Latin1_General_CP1_CI_AS", -1), nil
	case "ISANSINULLDEFAULT":
		return NewInt(0), nil
	case "ISANSIPADDINGON":
		return NewInt(0), nil
	case "ISANSIWARNINGSON":
		return NewInt(1), nil
	case "ISAABORTNULLON":
		return NewInt(0), nil
	case "ISCONCATANULLYIELDSNULLON":
		return NewInt(1), nil
	case "ISNUMERICROUNDABORTON":
		return NewInt(0), nil
	case "ISQUOTEDIDENTIFIERSON":
		return NewInt(1), nil
	case "ISRECURSIVETRGGERSON":
		return NewInt(0), nil
	case "ISCURSORCLOSEONCOMMIT":
		return NewInt(0), nil
	case "ISLOCALARBORTROUNDOFF":
		return NewInt(0), nil
	case "ISFULLTEXT":
		return NewInt(0), nil
	case "ISPARAMETERIZATION_FORCED":
		return NewInt(0), nil
	case "STATUS":
		return NewVarChar("ONLINE", -1), nil
	case "UPDATEABILITY":
		return NewVarChar("READ_WRITE", -1), nil
	case "USERACCESS":
		return NewVarChar("MULTI_USER", -1), nil
	case "RECOVERY":
		return NewVarChar("FULL", -1), nil
	case "VERSION":
		return NewInt(904), nil
	default:
		return Null(TypeVarChar), nil
	}
}

// fnTypeProperty returns type property information.
func fnTypeProperty(args []Value) (Value, error) {
	if len(args) < 2 {
		return Null(TypeInt), nil
	}

	if args[0].IsNull || args[1].IsNull {
		return Null(TypeInt), nil
	}

	prop := strings.ToUpper(args[1].AsString())

	switch prop {
	case "PRECISION":
		return NewInt(0), nil
	case "SCALE":
		return NewInt(0), nil
	case "ALLOWSNULL":
		return NewInt(1), nil
	case "USESANSIPADDING":
		return NewInt(0), nil
	default:
		return Null(TypeInt), nil
	}
}

// fnIndexProperty returns index property information.
func fnIndexProperty(args []Value) (Value, error) {
	if len(args) < 3 {
		return Null(TypeInt), nil
	}

	if args[2].IsNull {
		return Null(TypeInt), nil
	}

	prop := strings.ToUpper(args[2].AsString())

	switch prop {
	case "INDEXDEPTH":
		return NewInt(1), nil
	case "ISUNIQUE":
		return NewInt(0), nil
	case "ISCLUSTERED":
		return NewInt(0), nil
	case "ISDISABLED":
		return NewInt(0), nil
	case "ISHYPOTHETICAL":
		return NewInt(0), nil
	case "ISPADINDEX":
		return NewInt(0), nil
	case "ISPAGELOCKDISALLOWED":
		return NewInt(0), nil
	case "ISROWLOCKDISALLOWED":
		return NewInt(0), nil
	case "ISSTATISTICS":
		return NewInt(0), nil
	case "ISAUTOSTATISTICS":
		return NewInt(1), nil
	default:
		return Null(TypeInt), nil
	}
}

// fnHasPermsById checks permissions (always returns 1 for compatibility).
func fnHasPermsById(args []Value) (Value, error) {
	// Always return 1 (has permission) for compatibility
	return NewInt(1), nil
}

// fnSUserSName returns the login name for a security ID.
func fnSUserSName(args []Value) (Value, error) {
	// Return a default user name
	return NewVarChar("sa", -1), nil
}

// fnSUserName returns the user name.
func fnSUserName(args []Value) (Value, error) {
	return NewVarChar("sa", -1), nil
}

// fnSystemUser returns the current login name.
func fnSystemUser(args []Value) (Value, error) {
	return NewVarChar("sa", -1), nil
}

// fnUserName returns the database user name.
func fnUserName(args []Value) (Value, error) {
	if len(args) == 0 {
		return NewVarChar("dbo", -1), nil
	}
	return NewVarChar("dbo", -1), nil
}

// fnCurrentUser returns the current user name.
func fnCurrentUser(args []Value) (Value, error) {
	return NewVarChar("dbo", -1), nil
}

// fnSessionUser returns the session user name.
func fnSessionUser(args []Value) (Value, error) {
	return NewVarChar("dbo", -1), nil
}

// fnOriginalLogin returns the original login name.
func fnOriginalLogin(args []Value) (Value, error) {
	return NewVarChar("sa", -1), nil
}

// fnAppName returns the application name.
func fnAppName(args []Value) (Value, error) {
	return NewVarChar("aul-client", -1), nil
}

// fnHostName returns the host name.
func fnHostName(args []Value) (Value, error) {
	return NewVarChar("localhost", -1), nil
}
