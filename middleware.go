package main

import (
	"net/http"
	"time"

	"github.com/dchest/uniuri"
	"github.com/raggaer/castro/app/models"
	"github.com/raggaer/castro/app/util"
	"github.com/ulule/limiter"
	"golang.org/x/net/context"
)

// microtimeHandler used to record all requests time spent
type microtimeHandler struct{}

// csrfHandler used to add a token to all requests
type csrfHandler struct{}

// sessionHandler used for application session
type sessionHandler struct{}

// securityHandler used to set some headers
type securityHandler struct{}

// rateLimitHandler used for rate-limiting
type rateLimitHandler struct {
	Limiter *limiter.Limiter
}

// newRateLimitHandler creates and returns a new rateLimitHandler instance
func newRateLimitHandler(limiter *limiter.Limiter) *rateLimitHandler {
	return &rateLimitHandler{limiter}
}

func (r *rateLimitHandler) ServeHTTP(w http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	// IP data holder
	ip := ""

	// Check if behind proxy
	if util.Config.SSL.Proxy {

		// Get address from X-Forwarded-For
		ip = req.Header.Get("X-Forwarded-For")

	} else {

		// Get address from RemoteAddress
		ip = req.RemoteAddr
	}

	// Get rate-limit context
	ctx, err := r.Limiter.Get(ip)

	if err != nil {
		http.Error(w, "Cannot get rate-limit instance", 500)
		return
	}

	// Check for limit
	if ctx.Reached {
		http.Error(w, "Rate-limit reached", 500)
		return
	}

	// Execute next handler
	next(w, req)
}

// newSecurityHandler creates and returns a new securityHandler instance
func newSecurityHandler() *securityHandler {
	return &securityHandler{}
}

func (s *securityHandler) ServeHTTP(w http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	// Set Strict-Transport-Security header if SSL
	if util.Config.IsSSL() {

		// Set header
		w.Header().Set("Strict-Transport-Security", util.Config.Security.STS)
	}

	// Set Engine header
	w.Header().Set("Engine", "Castro")

	// Set X-XSS-Protection header
	w.Header().Set("X-XSS-Protection", util.Config.Security.XSS)

	// Set X-Frame-Options header
	w.Header().Set("X-Frame-Options", util.Config.Security.Frame)

	// Set X-Content-Type-Options header
	w.Header().Set("X-Content-Type-Options", util.Config.Security.ContentType)

	// Set Referrer-Policy header
	w.Header().Set("Referrer-Policy", util.Config.Security.ReferrerPolicy)

	// Set X-Permitted-Cross-Domain-Policies header
	w.Header().Set("X-Permitted-Cross-Domain-Policies", util.Config.Security.CrossDomainPolicy)

	// Set Content-Security-Policy header
	w.Header().Set(
		"Content-Security-Policy",
		util.Config.CSP(),
	)

	// Execute next handler
	next(w, req)
}

// newSessionHandler creates and returns a new sessionHandler instance
func newSessionHandler() *sessionHandler {
	return &sessionHandler{}
}

func (s *sessionHandler) ServeHTTP(w http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	// Get application cookie
	cookie, err := req.Cookie(util.Config.Cookies.Name)

	if err != nil {

		// Cookie data
		v := make(map[string]interface{})

		// Set issuer field
		v["issuer"] = "Castro"

		// Encode cookie value
		encoded, err := util.SessionStore.Encode(util.Config.Cookies.Name, v)

		if err != nil {
			util.Logger.Errorf("Cannot encode cookie value: %v", err)
			return
		}

		// Create cookie
		c := &http.Cookie{
			Name:     util.Config.Cookies.Name,
			Value:    encoded,
			Path:     "/",
			MaxAge:   util.Config.Cookies.MaxAge,
			Secure:   util.Config.IsSSL(),
			HttpOnly: true,
		}

		// Set cookie
		http.SetCookie(w, c)

		// Create new context with cookie value
		ctx := context.WithValue(req.Context(), "session", v)

		// Run next handler
		next(w, req.WithContext(ctx))

		return
	}

	// Cookie data holder
	v := make(map[string]interface{})

	// Decode cookie
	if err := util.SessionStore.Decode(
		util.Config.Cookies.Name,
		cookie.Value,
		&v,
	); err != nil {

		util.Logger.Errorf("Cannot decode cookie value: %v", err)
		return
	}

	// Check issuer
	if v["issuer"].(string) != "Castro" {
		return
	}

	// Create new context with cookie value
	ctx := context.WithValue(req.Context(), "session", v)

	// Run next handler
	next(w, req.WithContext(ctx))
}

// newCsrfHandler creates and returns a new csrfHandler instance
func newCsrfHandler() *csrfHandler {
	return &csrfHandler{}
}

func (c *csrfHandler) ServeHTTP(w http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	// Get session
	session, ok := req.Context().Value("session").(map[string]interface{})

	if !ok {
		return
	}

	// Get token
	token, ok := session["csrf-token"].(*models.CsrfToken)

	if !ok {

		// Check if request is valid
		if req.Method == http.MethodPost {
			return
		}

		// Create token
		tkn := models.CsrfToken{
			Token: uniuri.New(),
			At:    time.Now(),
		}

		// Set session value
		session["csrf-token"] = &tkn

		// Encode session
		encoded, err := util.SessionStore.Encode(util.Config.Cookies.Name, session)

		if err != nil {
			util.Logger.Errorf("Cannot encode session: %v", err)
		}

		// Create cookie
		c := &http.Cookie{
			Name:     util.Config.Cookies.Name,
			Value:    encoded,
			Path:     "/",
			MaxAge:   util.Config.Cookies.MaxAge,
			Secure:   util.Config.IsSSL(),
			HttpOnly: true,
		}

		// Set cookie
		http.SetCookie(w, c)

		// Create context
		ctx := context.WithValue(req.Context(), "csrf-token", &tkn)

		// Run next handler
		next(w, req.WithContext(ctx))

		return
	}

	// Check if valid token
	if req.Method == http.MethodPost && req.FormValue("_csrf") != token.Token {
		return
	}

	// Check if token  is old
	if time.Now().Before(token.At) {

		// Create new token
		token.Token = uniuri.New()
		token.At = time.Now()

		// Encode session
		encoded, err := util.SessionStore.Encode(util.Config.Cookies.Name, session)

		if err != nil {
			util.Logger.Errorf("Cannot encode session: %v", err)
		}

		// Create cookie
		c := util.SessionCookie(encoded)

		// Set cookie
		http.SetCookie(w, c)
	}

	// Create context
	ctx := context.WithValue(req.Context(), "csrf-token", token)

	// Run next handler
	next(w, req.WithContext(ctx))
}

// newMicrotimeHandler creates and returns a new microtimeHandler instance
func newMicrotimeHandler() *microtimeHandler {
	return &microtimeHandler{}
}

// ServeHTTP makes microtimeHandler compatible with negroni
func (m *microtimeHandler) ServeHTTP(w http.ResponseWriter, req *http.Request, next http.HandlerFunc) {
	// Set timestamp on the request context
	ctx := context.WithValue(req.Context(), "microtime", time.Now())

	// Execute next handler
	next(w, req.WithContext(ctx))
}
