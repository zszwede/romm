package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// Sync-device endpoints (DeviceSchema). The web player registers the browser as
// a device so saves/states can sync; ported from backend/endpoints/device.

func (s *Server) deviceUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return 0, false
	}
	return uid, true
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.deviceUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name          string `json:"name"`
		Platform      string `json:"platform"`
		Client        string `json:"client"`
		ClientVersion string `json:"client_version"`
		Hostname      string `json:"hostname"`
		SyncMode      string `json:"sync_mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	d, err := s.st.CreateDevice(ctx, uid, store.DeviceCreate{
		Name: body.Name, Platform: body.Platform, Client: body.Client,
		ClientVersion: body.ClientVersion, Hostname: body.Hostname, SyncMode: body.SyncMode,
	})
	if err != nil || d == nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"device_id": d.ID, "name": d.Name, "created_at": d.CreatedAt,
	})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.deviceUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	devices, err := s.st.ListDevices(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *Server) onlineDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.deviceUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, []string{})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.deviceUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	gone, err := s.st.DeleteDevice(ctx, uid, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.deviceUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	d, err := s.st.GetDevice(ctx, uid, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if d == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, d)
}
