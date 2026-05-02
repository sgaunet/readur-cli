package errors

// Exit codes — the authoritative surface documented in
// specs/001-readur-upload-cli/contracts/exit-codes.md. Scripts consume
// these values; changing any of them is a breaking change per the
// constitution's versioning policy.
const (
	CodeOK        = 0
	CodeGeneric   = 1
	CodeUsage     = 2
	CodeAuth      = 3
	CodeNetwork   = 4
	CodePartial   = 5
	CodeNoInput   = 66
	CodeCantCreat = 73
	CodeConfig    = 78
)

// codeNames maps exit code values to their stable string names.
var codeNames = map[int]string{
	CodeOK:        "OK",
	CodeGeneric:   "GENERIC",
	CodeUsage:     "USAGE",
	CodeAuth:      "AUTH",
	CodeNetwork:   "NETWORK",
	CodePartial:   "PARTIAL",
	CodeNoInput:   "NOINPUT",
	CodeCantCreat: "CANTCREAT",
	CodeConfig:    "CONFIG",
}

// Name returns the exported constant name for an exit code, used in
// verbose log output and JSON error objects.
func Name(code int) string {
	if s, ok := codeNames[code]; ok {
		return s
	}
	return "UNKNOWN"
}
