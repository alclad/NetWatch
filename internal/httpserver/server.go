package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"netwatch/internal/monitor"
	"netwatch/web"
)

// Handler собирает HTTP-маршруты: REST API, SSE и встроенный статический UI.
// Совместим с Go 1.21+ (без шаблонов ServeMux 1.22).
func Handler(m *monitor.Monitor) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		respond(w, http.StatusOK, m.Snapshot())
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPut) {
			return
		}
		var in monitor.Settings
		if !decode(w, r, &in) {
			return
		}
		v, err := m.UpdateSettings(in)
		result(w, v, err)
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		serveSSE(w, r, m)
	})
	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		var in monitor.DeviceInput
		if !decode(w, r, &in) {
			return
		}
		v, err := m.AddDevice(in)
		result(w, v, err)
	})
	mux.HandleFunc("/api/devices/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
		// Суффикс-действие poweroff: POST /api/devices/{id}/poweroff
		if action, hasAction := strings.CutSuffix(id, "/poweroff"); hasAction {
			if !method(w, r, http.MethodPost) {
				return
			}
			if action == "" || strings.Contains(action, "/") {
				http.NotFound(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			err := m.PowerOff(ctx, action)
			switch {
			case err == nil:
				respond(w, http.StatusOK, map[string]string{"ok": "команда выключения отправлена"})
			case errors.Is(err, monitor.ErrNotFound):
				result(w, nil, err)
			case errors.Is(err, monitor.ErrPowerUnavailable):
				respond(w, http.StatusNotImplemented, map[string]string{"error": err.Error()})
			default:
				respond(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			}
			return
		}
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPut:
			var in monitor.DeviceInput
			if !decode(w, r, &in) {
				return
			}
			v, err := m.UpdateDevice(id, in)
			result(w, v, err)
		case http.MethodDelete:
			result(w, map[string]bool{"ok": true}, m.DeleteDevice(id))
		default:
			w.Header().Set("Allow", "PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	assets, err := fs.Sub(web.FS, "static")
	if err != nil {
		panic(err)
	}
	// no-store: статика встроена в бинарник и меняется с каждой версией —
	// браузер не должен показывать устаревший app.js/style.css после обновления.
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.FileServer(http.FS(assets)).ServeHTTP(w, r)
	})
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !method(w, r, http.MethodGet) {
			return
		}
		b, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "UI недоступен", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "cross-origin request denied", http.StatusForbidden)
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-site request denied", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func serveSSE(w http.ResponseWriter, r *http.Request, m *monitor.Monitor) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := m.Subscribe()
	defer cancel()

	send := func(s monitor.State) bool {
		b, err := json.Marshal(s)
		if err != nil {
			return false
		}
		if _, err := io.WriteString(w, "event: state\ndata: "+string(b)+"\n\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send(m.Snapshot()) {
		return
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case s, ok := <-ch:
			if !ok || !send(s) {
				return
			}
		case <-heartbeat.C:
			if !send(m.Snapshot()) {
				return
			}
		}
	}
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "Некорректный JSON: " + err.Error()})
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		respond(w, http.StatusBadRequest, map[string]string{"error": "Ожидается один JSON-объект"})
		return false
	}
	return true
}

func result(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, monitor.ErrNotFound) {
			code = http.StatusNotFound
		}
		respond(w, code, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, v)
}

func respond(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
