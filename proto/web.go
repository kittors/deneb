package proto

// The Web API (/api/v1, M3–M4): what the browser app, the Web client and
// the admin pages, asks the control server. The JSON is the device API's
// (UserView, Op, OpResult, StreamFrame, RelayUsage); a session cookie
// takes the place of a device's token, and there is no "this device".
const (
	PathWebLoginEmail = "/api/v1/login/email" // POST WebLoginRequest{email} → WebLoginResult, step "code"
	PathWebLoginCode  = "/api/v1/login/code"  // POST WebLoginRequest{email, code} → WebLoginResult, step "done"; sets the cookie
	PathWebLogin      = "/api/v1/login"       // POST either of the two above, by whether code is there
	PathWebLogout     = "/api/v1/logout"      // POST (no body) → {}; clears it
	PathWebUserView   = "/api/v1/userview"    // GET → UserView
	PathWebStream     = "/api/v1/stream"      // GET → NDJSON of StreamFrame: userView and keepAlive only
	PathWebOps        = "/api/v1/ops"         // POST Op → OpResult
	PathWebRelayUsage = "/api/v1/relay-usage" // GET → RelayUsage
	PathWebInvite     = "/api/v1/invite"      // POST InviteLookup → InvitePreview; works logged out too
	PathWebInvites    = "/api/v1/invites/"    // GET + <token> → InvitePreview; works logged out too
	// PathWebOperatorUsage is the ops page's numbers, for the deployment's
	// operators only (UserView.Operator); anyone else gets 403.
	PathWebOperatorUsage = "/api/v1/operator/relay-usage" // GET → OperatorUsage
)

// WebSessionCookie is the session cookie: HttpOnly, SameSite=Lax, Secure
// over HTTPS, for 30 days. The server keeps only its hash.
const WebSessionCookie = "deneb_session"

// WebLoginRequest is the login form: the email first, then the email and
// its code.
type WebLoginRequest struct {
	Email string `json:"email"`
	Code  string `json:"code,omitempty"`
}

// WebLoginResult is where the login stands.
type WebLoginResult struct {
	// Step is "code" (the email has an account: ask for its code) or
	// "done" (logged in, the cookie set).
	Step string `json:"step"`
	// Account says whose the code is for: "登录到阿杰（jie@deneb.test）".
	Account string `json:"account,omitempty"`
	// Person is who is logged in now, once the step is "done".
	Person PersonID `json:"person,omitempty"`
}
