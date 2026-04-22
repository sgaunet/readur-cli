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

// Name returns the exported constant name for an exit code, used in
// verbose log output and JSON error objects.
func Name(code int) string {
	switch code {
	case CodeOK:
		return "OK"
	case CodeGeneric:
		return "GENERIC"
	case CodeUsage:
		return "USAGE"
	case CodeAuth:
		return "AUTH"
	case CodeNetwork:
		return "NETWORK"
	case CodePartial:
		return "PARTIAL"
	case CodeNoInput:
		return "NOINPUT"
	case CodeCantCreat:
		return "CANTCREAT"
	case CodeConfig:
		return "CONFIG"
	default:
		return "UNKNOWN"
	}
}
