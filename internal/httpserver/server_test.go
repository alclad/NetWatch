package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"netwatch/internal/monitor"
)

type stubPinger struct{}

func (stubPinger) Ping(ctx context.Context, address string) (time.Duration, error) {
	return time.Millisecond, nil
}

func newTestHandler(t *testing.T) (http.Handler, *monitor.Monitor) {
	t.Helper()
	m, err := monitor.New("", stubPinger{})
	if err != nil {
		t.Fatal(err)
	}
	return Handler(m), m
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestStateAndDeviceAPI(t *testing.T) {
	h, _ := newTestHandler(t)

	w := doJSON(t, h, "GET", "/api/state", "")
	if w.Code != 200 {
		t.Fatalf("GET /api/state: %d", w.Code)
	}

	w = doJSON(t, h, "POST", "/api/devices", `{"name":"srv","address":"10.0.0.1","url":"https://srv.local","group":"ADM"}`)
	if w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	var dev monitor.Device
	if err := json.Unmarshal(w.Body.Bytes(), &dev); err != nil {
		t.Fatal(err)
	}
	if dev.ID == "" || dev.Status != "unknown" || dev.Group != "ADM" {
		t.Fatalf("device: %+v", dev)
	}

	w = doJSON(t, h, "PUT", "/api/devices/"+dev.ID, `{"name":"srv2","address":"10.0.0.2","url":""}`)
	if w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}

	w = doJSON(t, h, "POST", "/api/devices", `{"name":"","address":""}`)
	if w.Code != 400 {
		t.Fatalf("невалидное устройство принято: %d", w.Code)
	}

	w = doJSON(t, h, "DELETE", "/api/devices/"+dev.ID, "")
	if w.Code != 200 {
		t.Fatalf("DELETE: %d", w.Code)
	}
	w = doJSON(t, h, "DELETE", "/api/devices/"+dev.ID, "")
	if w.Code != 404 {
		t.Fatalf("повторный DELETE: %d", w.Code)
	}
}

func TestSettingsAPI(t *testing.T) {
	h, _ := newTestHandler(t)
	w := doJSON(t, h, "PUT", "/api/settings", `{"interval_seconds":10,"timeout_seconds":3,"failure_threshold":2,"concurrency":8}`)
	if w.Code != 200 {
		t.Fatalf("PUT settings: %d %s", w.Code, w.Body)
	}
	w = doJSON(t, h, "PUT", "/api/settings", `{"interval_seconds":0,"timeout_seconds":0,"failure_threshold":0,"concurrency":0}`)
	if w.Code != 400 {
		t.Fatalf("невалидные настройки приняты: %d", w.Code)
	}
}

func TestUnknownJSONFieldsRejected(t *testing.T) {
	h, _ := newTestHandler(t)
	w := doJSON(t, h, "POST", "/api/devices", `{"name":"x","address":"1.1.1.1","evil":true}`)
	if w.Code != 400 {
		t.Fatalf("неизвестные поля приняты: %d", w.Code)
	}
}

func TestIndexAndStatic(t *testing.T) {
	h, _ := newTestHandler(t)
	w := doJSON(t, h, "GET", "/", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("index: %d %v", w.Code, w.Header())
	}
	if !strings.Contains(w.Body.String(), "Ping Monitor") {
		t.Fatal("index.html не содержит ожидаемой разметки")
	}
	w = doJSON(t, h, "GET", "/static/app.js", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "EventSource") {
		t.Fatalf("app.js: %d", w.Code)
	}
	w = doJSON(t, h, "GET", "/static/style.css", "")
	if w.Code != 200 {
		t.Fatalf("style.css: %d", w.Code)
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	h, _ := newTestHandler(t)
	r := httptest.NewRequest("POST", "/api/devices", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin POST не отклонён: %d", w.Code)
	}
}

func TestSSEStream(t *testing.T) {
	h, m := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type: %q", ct)
	}
	if _, err := m.AddDevice(monitor.DeviceInput{Name: "x", Address: "10.3.3.3"}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 65536)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "event: state") {
		t.Fatalf("нет SSE-события: %q", buf[:n])
	}
}
