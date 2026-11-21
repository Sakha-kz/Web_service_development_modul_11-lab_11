package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func request(h http.Handler, method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoutes(t *testing.T) {
	h := NewApp().Handler()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/", 200}, {"GET", "/about", 200}, {"GET", "/static/style.css", 200},
		{"GET", "/missing", 404}, {"POST", "/", 405}, {"POST", "/about", 405},
		{"GET", "/tasks", 405}, {"GET", "/tasks/complete", 405}, {"GET", "/tasks/delete", 405}, {"PUT", "/tasks", 405},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := request(h, tc.method, tc.path, nil)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
		})
	}
}

func assertRedirect(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("expected redirect to /; got %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestMalformedForm(t *testing.T) {
	h := NewApp().Handler()
	r := httptest.NewRequest("POST", "/tasks", strings.NewReader("x=%zz"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("got %d", w.Code)
	}
}

// Реальные одновременные запросы проверяют защиту состояния mutex-ом.
func TestConcurrentAdds(t *testing.T) {
	a := NewApp()
	h := a.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			form := url.Values{"title": {fmt.Sprint(i)}}
			if w := request(h, "POST", "/tasks", form); w.Code != 303 {
				t.Errorf("status %d", w.Code)
			}
			request(h, "GET", "/", nil)
		}(i)
	}
	wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tasks) != 24 {
		t.Fatalf("count %d", len(a.tasks))
	}
	seen := map[int]bool{}
	for _, item := range a.tasks {
		if seen[item.ID] {
			t.Fatalf("duplicate ID %d", item.ID)
		}
		seen[item.ID] = true
	}
}

func TestTaskFlow(t *testing.T) {
	a := NewApp()
	h := a.Handler()
	for _, title := range []string{"", "  "} {
		if w := request(h, "POST", "/tasks", url.Values{"title": {title}}); w.Code != 400 {
			t.Fatalf("empty title: %d", w.Code)
		}
	}
	if len(a.tasks) != 0 {
		t.Fatal("empty task created")
	}
	assertRedirect(t, request(h, "POST", "/tasks", url.Values{"title": {" <script>work</script> "}}))
	assertRedirect(t, request(h, "POST", "/tasks", url.Values{"title": {"Вторая"}}))
	for _, path := range []string{"/tasks/complete", "/tasks/delete"} {
		for _, id := range []string{"", "x", "0", "-1", "999999999999999999999999"} {
			if w := request(h, "POST", path, url.Values{"id": {id}}); w.Code != 400 {
				t.Fatalf("%s id %s: %d", path, id, w.Code)
			}
		}
		if w := request(h, "POST", path, url.Values{"id": {"999"}}); w.Code != 404 {
			t.Fatalf("missing ID: %d", w.Code)
		}
	}
	assertRedirect(t, request(h, "POST", "/tasks/complete", url.Values{"id": {"1"}}))
	body := request(h, "GET", "/", nil).Body.String()
	if !strings.Contains(body, "Всего: 2 · Выполнено: 1 · Не выполнено: 1") {
		t.Fatal("wrong statistics")
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("HTML not escaped")
	}
	assertRedirect(t, request(h, "POST", "/tasks/complete", url.Values{"id": {"1"}}))
	if a.tasks[0].Completed {
		t.Fatal("toggle back failed")
	}
	assertRedirect(t, request(h, "POST", "/tasks/delete", url.Values{"id": {"1"}}))
	assertRedirect(t, request(h, "POST", "/tasks", url.Values{"title": {"Третья"}}))
	if a.tasks[1].ID != 3 {
		t.Fatal("ID reused")
	}
	for i := 0; i < 2; i++ {
		body = request(h, "GET", "/", nil).Body.String()
		if !strings.Contains(body, "Всего: 2 · Выполнено: 0 · Не выполнено: 2") || strings.Contains(body, "script") {
			t.Fatal("delete/refresh failed")
		}
	}
	assertRedirect(t, request(h, "POST", "/tasks/delete", url.Values{"id": {"2"}}))
	assertRedirect(t, request(h, "POST", "/tasks/delete", url.Values{"id": {"3"}}))
	if w := request(h, "POST", "/tasks/delete", url.Values{"id": {"3"}}); w.Code != 404 {
		t.Fatal("repeated delete must fail")
	}
	if !strings.Contains(request(h, "GET", "/", nil).Body.String(), "Всего: 0") {
		t.Fatal("empty count")
	}
}
