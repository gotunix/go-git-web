package main

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

type Site struct {
	Host     string `yaml:"host"`
	RepoPath string `yaml:"repo_path"`
	Branch   string `yaml:"branch"`
}

type Config struct {
	Sites []Site `yaml:"sites"`
}

var routeMap = make(map[string]Site)

func loadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	for _, site := range cfg.Sites {
		routeMap[site.Host] = site
		log.Printf("Mapped host %s -> repo %s (branch: %s)", site.Host, site.RepoPath, site.Branch)
	}

	return nil
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		http.Error(w, "Build info not available", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "Go Version: %s\n", info.GoVersion)
	fmt.Fprintf(w, "Main Module: %s\n", info.Main.Path)
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		fmt.Fprintf(w, "Main Version: %s\n", info.Main.Version)
	}
	fmt.Fprintf(w, "\nDependencies:\n")
	for _, dep := range info.Deps {
		fmt.Fprintf(w, "%-40s %s\n", dep.Path, dep.Version)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	// Extract the host. Ensure standard behavior matches.
	host := r.Host

	siteCfg, exists := routeMap[host]
	// If it didn't match and contains a port, try falling back to just the hostname
	if !exists && strings.Contains(host, ":") {
		hostWithoutPort := strings.Split(host, ":")[0]
		siteCfg, exists = routeMap[hostWithoutPort]
	}

	if !exists {
		http.Error(w, fmt.Sprintf("404 Not Found (Unmapped Host: %s)", host), http.StatusNotFound)
		return
	}

	repoPath := siteCfg.RepoPath
	reqPath := r.URL.Path
	if reqPath == "/" {
		reqPath = "index.html"
	}
	reqPath = strings.TrimPrefix(reqPath, "/")

	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		log.Printf("Failed to open repo at %s: %v", repoPath, err)
		http.Error(w, "500 Internal Server Error (Repo inaccessible)", http.StatusInternalServerError)
		return
	}

	var ref *plumbing.Reference
	if siteCfg.Branch != "" {
		branchName := plumbing.NewBranchReferenceName(siteCfg.Branch)
		ref, err = repo.Reference(branchName, true)
		if err != nil {
			log.Printf("Failed to get branch %s for repo %s: %v", siteCfg.Branch, repoPath, err)
			http.Error(w, "500 Internal Server Error (Branch not found)", http.StatusInternalServerError)
			return
		}
	} else {
		// Try resolving main or master if HEAD is detached, but HEAD usually works reliably for bare repos pointing to a branch
		ref, err = repo.Head()
		if err != nil {
			// Fallback for empty/just-created bare repos
			if err == plumbing.ErrReferenceNotFound {
				http.Error(w, "404 Not Found in Git Repo - Repo is Empty", http.StatusNotFound)
				return
			}
			log.Printf("Failed to get HEAD for repo %s: %v", repoPath, err)
			http.Error(w, "500 Internal Server Error (No HEAD commit)", http.StatusInternalServerError)
			return
		}
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		log.Printf("Failed to get commit object: %v", err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}

	tree, err := commit.Tree()
	if err != nil {
		log.Printf("Failed to get tree: %v", err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}

	file, err := tree.File(reqPath)
	// Try appending index.html if resolving it natively failed (i.e. directory was requested but slashes were messy)
	if err == object.ErrFileNotFound {
		reqPath = filepath.Join(reqPath, "index.html")
		file, err = tree.File(reqPath)
	}

	if err == object.ErrFileNotFound {
		http.Error(w, "404 Not Found in Git Repo", http.StatusNotFound)
		return
	} else if err != nil {
		log.Printf("Failed to resolve file %s: %v", reqPath, err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}

	reader, err := file.Reader()
	if err != nil {
		log.Printf("Failed to open file reader for %s: %v", reqPath, err)
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	ext := filepath.Ext(reqPath)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)

	_, err = io.Copy(w, reader)
	if err != nil {
		log.Printf("Error streaming file %s: %v", reqPath, err)
	}
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		recorder := &responseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default to 200
		}

		next.ServeHTTP(recorder, r)

		log.Printf("[%s] %s - \"%s %s %s\" %d %d %v",
			r.Host,
			r.RemoteAddr,
			r.Method,
			r.RequestURI,
			r.Proto,
			recorder.statusCode,
			recorder.size,
			time.Since(start),
		)
	})
}

func main() {
	if err := loadConfig("config.yaml"); err != nil {
		log.Fatalf("Failed to load config.yaml: %v", err)
	}

	http.HandleFunc("/__versions__", versionHandler)
	http.HandleFunc("/__version__", versionHandler)
	http.HandleFunc("/", handler)

	port := "8080"
	log.Printf("Starting Git-backed Web Server on :%s", port)
	if err := http.ListenAndServe(":"+port, loggingMiddleware(http.DefaultServeMux)); err != nil {
		log.Fatal(err)
	}
}
