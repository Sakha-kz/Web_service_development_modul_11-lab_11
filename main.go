package main

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

//go:embed templates/*.html static/*
var assets embed.FS

// App хранит состояние одного экземпляра сервера.
// Mutex защищает срез: HTTP-сервер обрабатывает запросы параллельно.
type App struct {
	mu        sync.Mutex
	tasks     []Task
	nextID    int
	templates *template.Template
}

func NewApp() *App {
	return &App{nextID: 1, templates: template.Must(template.ParseFS(assets, "templates/*.html"))}
}

// Handler создаёт отдельный маршрутизатор; неизвестные адреса возвращают 404.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.homeHandler)
	mux.HandleFunc("GET /about", a.aboutHandler)
	mux.HandleFunc("POST /tasks", a.addHandler)
	mux.HandleFunc("POST /tasks/complete", a.changeHandler)
	mux.HandleFunc("POST /tasks/delete", a.changeHandler)
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	return mux
}

// render сначала заполняет буфер, чтобы при ошибке не отправить половину страницы.
func (a *App) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := a.templates.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template: %v", err)
		http.Error(w, "Ошибка формирования страницы", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (a *App) aboutHandler(w http.ResponseWriter, r *http.Request) {
	a.render(w, "about.html", nil)
}

// Ограничение относится к телу формы, чтобы сервер не читал неограниченный ввод.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректная или слишком большая форма", http.StatusBadRequest)
		return false
	}
	return true
}

func main() {
	app := NewApp()
	log.Println("Task Tracker: http://localhost:8080")
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", app.Handler()))
}

// Task соответствует структуре из лабораторного задания.
type Task struct {
	ID        int
	Title     string
	Completed bool
}

type PageData struct {
	Tasks          []Task
	TotalTasks     int
	CompletedTasks int
	PendingTasks   int
}

func (a *App) homeHandler(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	data := PageData{Tasks: append([]Task(nil), a.tasks...), TotalTasks: len(a.tasks)}
	for _, task := range a.tasks {
		if task.Completed {
			data.CompletedTasks++
		}
	}
	data.PendingTasks = data.TotalTasks - data.CompletedTasks
	a.mu.Unlock()
	a.render(w, "index.html", data)
}

// addTask изменяет исходный App через получатель-указатель.
func (a *App) addTask(title string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tasks = append(a.tasks, Task{ID: a.nextID, Title: title})
	a.nextID++
}

// findTask возвращает индекс, а не адрес копии элемента из range.
// Вызывается только при удерживаемом a.mu.
func (a *App) findTask(id int) int {
	for i := range a.tasks {
		if a.tasks[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) toggleTask(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.findTask(id)
	if i < 0 {
		return false
	}
	a.tasks[i].Completed = !a.tasks[i].Completed
	return true
}

func (a *App) deleteTask(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.findTask(id)
	if i < 0 {
		return false
	}
	a.tasks = append(a.tasks[:i], a.tasks[i+1:]...)
	return true
}

func (a *App) addHandler(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "Название задачи не должно быть пустым", http.StatusBadRequest)
		return
	}
	a.addTask(title)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) changeHandler(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil || id <= 0 {
		http.Error(w, "ID должен быть положительным целым числом", http.StatusBadRequest)
		return
	}
	var found bool
	if r.URL.Path == "/tasks/complete" {
		found = a.toggleTask(id)
	} else {
		found = a.deleteTask(id)
	}
	if !found {
		http.Error(w, "Задача не найдена", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
