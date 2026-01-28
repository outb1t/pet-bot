package main

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"

	"pet.outbid.goapp/db"
)

func startWebServer() {
	username := getStringFromEnv("BASIC_AUTH_USERNAME")
	password := getStringFromEnv("BASIC_AUTH_PASSWORD")

	fs := http.FileServer(http.Dir("web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", basicAuth(username, password, indexHandler))
	http.HandleFunc("/save", basicAuth(username, password, saveHandler))
	port := os.Getenv("WEB_SERVER_PORT")
	if port == "" {
		port = "8080"
	}
	log.Println("Starting web server on : " + port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("Failed to start web server: %v", err)
	}
}

func basicAuth(username, password string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		const prefix = "Basic "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		credentials := strings.SplitN(string(decoded), ":", 2)
		if len(credentials) != 2 {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		reqUsername, reqPassword := credentials[0], credentials[1]
		if reqUsername != username || reqPassword != password {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		handler.ServeHTTP(w, r)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	templateContent, err := os.ReadFile("web/index.html")
	if err != nil {
		fmt.Println(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var formTemplate = template.Must(template.New("form").Parse(string(templateContent)))

	promptText, err := db.GetSystemPrompt(false)
	if err != nil {
		fmt.Println(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	data := struct {
		Prompt string
	}{
		Prompt: promptText,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := formTemplate.Execute(w, data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func saveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	prompt := r.FormValue("prompt")
	if prompt == "" {
		http.Error(w, "Prompt cannot be empty", http.StatusBadRequest)
		return
	}

	err := db.InsertPrompt(prompt, 1)
	if err != nil {
		http.Error(w, "Failed to save prompt", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
