package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/deviceauth"
	"github.com/zszwede/romm-go/internal/store"
)

// Device authorization flow endpoints, ported from backend/endpoints/device/auth.py.
//
// A device POSTs /init with its metadata and desired scopes and gets a
// device_code (secret, for polling) plus a user_code (short, for a QR). The
// user opens /pair/device in the web UI, the app GETs /pending/{user_code} to
// show the request, and POSTs /approve or /deny (both bearer-protected). The
// device meanwhile polls /token until it gets a token, access_denied, or an
// expiry. Flow state lives in an in-process deviceauth.Store (no Redis).

const (
	scopeMeRead  = "me.read"
	scopeMeWrite = "me.write"
)

// expiryMap mirrors client_tokens.EXPIRY_MAP.
var expiryMap = map[string]time.Duration{
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
	"1y":  365 * 24 * time.Hour,
}

// parseExpiry resolves an expires_in token to an absolute time. "" and "never"
// mean no expiry; an unknown value is an error (ok=false).
func parseExpiry(expiresIn string) (*time.Time, bool) {
	if expiresIn == "" || expiresIn == "never" {
		return nil, true
	}
	d, ok := expiryMap[expiresIn]
	if !ok {
		return nil, false
	}
	t := time.Now().UTC().Add(d)
	return &t, true
}

func (s *Server) deviceInit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientDeviceIdentifier string   `json:"client_device_identifier"`
		Name                   string   `json:"name"`
		Client                 string   `json:"client"`
		Platform               string   `json:"platform"`
		ClientVersion          string   `json:"client_version"`
		RequestedScopes        []string `json:"requested_scopes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}

	deviceCode := deviceauth.GenerateDeviceCode()
	userCode := deviceauth.GenerateUserCode()
	s.flows.StorePending(deviceCode, userCode, deviceauth.Pending{
		ClientDeviceIdentifier: body.ClientDeviceIdentifier,
		Name:                   body.Name,
		Client:                 body.Client,
		Platform:               body.Platform,
		ClientVersion:          body.ClientVersion,
		RequestedScopes:        body.RequestedScopes,
		Interval:               deviceauth.PollDefaultInterval,
	})
	path, pathComplete := deviceauth.BuildVerificationPaths(userCode)

	writeJSON(w, http.StatusCreated, map[string]any{
		"device_code":                  deviceCode,
		"user_code":                    userCode,
		"verification_path":            path,
		"verification_path_complete":   pathComplete,
		"expires_in":                   int(deviceauth.PendingTTL.Seconds()),
		"interval":                     deviceauth.PollDefaultInterval,
	})
}

func (s *Server) devicePending(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope(scopeMeRead) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userCode := deviceauth.NormalizeUserCode(r.PathValue("user_code"))
	deviceCode, ok := s.flows.ResolveDeviceCodeFromUserCode(userCode)
	if !ok {
		writeErr(w, http.StatusNotFound, "Unknown or expired code")
		return
	}
	p, status, ok := s.flows.LoadPending(deviceCode)
	if !ok {
		writeErr(w, http.StatusNotFound, "Unknown or expired code")
		return
	}
	if status != deviceauth.StatusPending {
		writeErr(w, http.StatusGone, "Code already "+string(status))
		return
	}

	requested := append([]string(nil), p.RequestedScopes...)
	deviceauthSort(requested)
	writeJSON(w, http.StatusOK, map[string]any{
		"client_device_identifier": p.ClientDeviceIdentifier,
		"name":                     p.Name,
		"client":                   p.Client,
		"platform":                 p.Platform,
		"client_version":           p.ClientVersion,
		"requested_scopes":         requested,
		"allowed_scopes":           deviceauth.Intersect(p.RequestedScopes, claims.Scopes),
		"expires_at":               s.flows.PendingExpiresAt(deviceCode),
	})
}

func (s *Server) deviceApprove(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope(scopeMeWrite) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		UserCode       string   `json:"user_code"`
		DeviceName     string   `json:"device_name"`
		ApprovedScopes []string `json:"approved_scopes"`
		ExpiresIn      string   `json:"expires_in"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}

	userCode := deviceauth.NormalizeUserCode(body.UserCode)
	deviceCode, ok := s.flows.ResolveDeviceCodeFromUserCode(userCode)
	if !ok {
		writeErr(w, http.StatusNotFound, "Unknown or expired code")
		return
	}
	p, status, ok := s.flows.LoadPending(deviceCode)
	if !ok {
		writeErr(w, http.StatusNotFound, "Unknown or expired code")
		return
	}
	if status != deviceauth.StatusPending {
		writeErr(w, http.StatusGone, "Code already "+string(status))
		return
	}

	allowed := deviceauth.Intersect(p.RequestedScopes, claims.Scopes)
	if len(body.ApprovedScopes) == 0 || !deviceauth.Subset(body.ApprovedScopes, allowed) {
		writeErr(w, http.StatusForbidden, "Approved scopes exceed what's allowed for this user")
		return
	}
	expiresAt, okExp := parseExpiry(body.ExpiresIn)
	if !okExp {
		writeErr(w, http.StatusUnprocessableEntity, "Invalid expires_in value")
		return
	}

	deviceName := body.DeviceName
	if deviceName == "" {
		deviceName = p.Name
	}
	approvedScopes := deviceauth.Intersect(body.ApprovedScopes, body.ApprovedScopes) // sort+dedupe
	rawToken := deviceauth.GenerateClientToken()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return
	}
	deviceID, err := s.st.RegisterDeviceToken(ctx, deviceTokenInput(uid, deviceName, p, rawToken, approvedScopes, expiresAt))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}

	s.flows.MarkApproved(deviceCode, deviceauth.Approved{
		RawToken:  rawToken,
		DeviceID:  deviceID,
		Scopes:    approvedScopes,
		ExpiresAt: expiresAt,
	})

	writeJSON(w, http.StatusOK, map[string]any{"device_id": deviceID, "device_name": deviceName})
}

func (s *Server) deviceDeny(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope(scopeMeWrite) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		UserCode string `json:"user_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	userCode := deviceauth.NormalizeUserCode(body.UserCode)
	deviceCode, ok := s.flows.ResolveDeviceCodeFromUserCode(userCode)
	if !ok {
		writeErr(w, http.StatusNotFound, "Unknown or expired code")
		return
	}
	_, status, ok := s.flows.LoadPending(deviceCode)
	if !ok {
		writeErr(w, http.StatusNotFound, "Unknown or expired code")
		return
	}
	if status != deviceauth.StatusPending {
		writeErr(w, http.StatusGone, "Code already "+string(status))
		return
	}
	s.flows.MarkDenied(deviceCode)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deviceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	p, status, ok := s.flows.LoadPending(body.DeviceCode)
	if !ok {
		// OAuth device-flow error codes travel in the body with a 400.
		writeErr(w, http.StatusBadRequest, "expired_token")
		return
	}

	switch status {
	case deviceauth.StatusPending:
		interval := p.Interval
		if interval == 0 {
			interval = deviceauth.PollDefaultInterval
		}
		if s.flows.PolledTooFast(body.DeviceCode, interval) {
			writeErr(w, http.StatusBadRequest, "slow_down")
			return
		}
		writeErr(w, http.StatusBadRequest, "authorization_pending")
	case deviceauth.StatusDenied:
		writeErr(w, http.StatusBadRequest, "access_denied")
	case deviceauth.StatusApproved:
		a, ok := s.flows.ConsumeApproved(body.DeviceCode)
		if !ok {
			writeErr(w, http.StatusBadRequest, "expired_token")
			return
		}
		resp := map[string]any{
			"access_token": a.RawToken,
			"device_id":    a.DeviceID,
			"scopes":       a.Scopes,
		}
		if a.ExpiresAt != nil {
			resp["expires_at"] = a.ExpiresAt
		} else {
			resp["expires_at"] = nil
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeErr(w, http.StatusBadRequest, "expired_token")
	}
}

func deviceTokenInput(uid int64, name string, p deviceauth.Pending, rawToken string, scopes []string, expiresAt *time.Time) store.DeviceTokenInput {
	return store.DeviceTokenInput{
		UserID:                 uid,
		DeviceID:               deviceauth.NewUUID4(),
		ClientDeviceIdentifier: p.ClientDeviceIdentifier,
		Name:                   name,
		Client:                 p.Client,
		Platform:               p.Platform,
		ClientVersion:          p.ClientVersion,
		HashedToken:            deviceauth.HashClientToken(rawToken),
		Scopes:                 joinScopes(scopes),
		ExpiresAt:              expiresAt,
	}
}

func joinScopes(scopes []string) string {
	out := ""
	for i, sc := range scopes {
		if i > 0 {
			out += " "
		}
		out += sc
	}
	return out
}

func deviceauthSort(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}
