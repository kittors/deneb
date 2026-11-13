package proto

import "time"

// The Web API (/api/v1, M3–M4): what the browser app, the Web client and
// the admin pages, asks the control server. The JSON is the device API's
// (UserView, Op, OpResult, StreamFrame, RelayUsage); a session cookie
// takes the place of a device's token, and there is no "this device".
const (
	PathWebLoginEmail = "/api/v1/login/email" // POST WebLoginRequest{email} → WebLoginResult, step "code"; a code goes out by mail
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
	// Logging in by the link in the code's mail (M7, LoginLinkURL;
	// design/plan/m7/login-link.md). The token goes in the body, never in
	// a URL.
	PathWebLoginLinkInfo = "/api/v1/login/link/info" // POST WebLoginLinkRequest → WebLoginLinkInfo; uses nothing up
	PathWebLoginLink     = "/api/v1/login/link"      // POST WebLoginLinkRequest → WebLoginResult, step "done"; sets the cookie
)

// WebLoginLinkRequest names a login link by its token: what follows the #
// of LoginLinkURL.
type WebLoginLinkRequest struct {
	Token string `json:"token"`
}

// WebLoginLinkInfo is what a login link would log in as, for the page that
// asks before it does: the email, and until when the link works.
type WebLoginLinkInfo struct {
	Account   string    `json:"account"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// WebSessionCookie is the session cookie: HttpOnly, SameSite=Lax, Secure
// over HTTPS, for 30 days. The server keeps only its hash.
const WebSessionCookie = "deneb_session"

// WebLoginRequest is the login form: the email first, then the email and
// its code. A code is six digits, mailed for each login: it works once,
// within CodeLife. (The deployment's test accounts, at reserved domains
// such as deneb.test, have a fixed code from its seed instead; nothing is
// mailed to them.)
type WebLoginRequest struct {
	Email string `json:"email"`
	Code  string `json:"code,omitempty"`
}

// WebLoginResult is where the login stands.
type WebLoginResult struct {
	// Step is "code" (type the code that went to the email) or "done"
	// (logged in, the cookie set). The code step answers the same for
	// every well-formed email, with an account or without, so no one
	// learns from it who has one (M7): a code goes out only if the email
	// can log in. A wrong code, or one for an email that cannot, is the
	// same 400.
	Step string `json:"step"`
	// Account is the email the code is for, as the server took it
	// (trimmed, lower case). It names no one: "登录到阿杰" would tell who
	// has an account.
	Account string `json:"account,omitempty"`
	// CodeLife and ResendAfter, on the code step, are how long a code
	// works and how long until asking again mails it again, in seconds:
	// "10 分钟内有效", "60 秒后可以重发". Asking sooner changes nothing.
	CodeLife    int `json:"codeLife,omitempty"`
	ResendAfter int `json:"resendAfter,omitempty"`
	// Person is who is logged in now, once the step is "done".
	Person PersonID `json:"person,omitempty"`
}
