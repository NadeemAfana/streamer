package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Http client that connects.
type client struct {
	fileName          string
	clientConnected   chan bool
	downloadCompleted chan bool
	receiving         bool
	receiver          *http.ResponseWriter
}

// Url where this service is hosted where clients will download the files (e.g., https://mydomain.com/streamer)
// For localhost, use http://localhost:3000 where 3000 is the local http listenter port.
var downloadBaseUrl = os.Getenv("DOWNLOAD_BASE_URL") // e.g., https://mydomain.com/streamer

// Local http listener port
var port = os.Getenv("PORT")

var validUserName = os.Getenv("USER_NAME")
var validPassword = os.Getenv("USER_PASSWORD")

// Route prefix
const prefix = "streamer"

const bufferSize = 1 << 15 // 32 KiB buffer.
var bufPool = sync.Pool{
	New: func() interface{} {
		buffer := make([]byte, bufferSize)
		return &buffer
	},
}

func main() {
	startTime := time.Now()

	if downloadBaseUrl == "" {
		log.Panic("DOWNLOAD_BASE_URL is empty")
	}
	if validUserName == "" {
		log.Panic("USER_NAME is empty")
	}

	if validPassword == "" {
		log.Panic("USER_PASSWORD is empty")
	}

	if port == "" {
		port = "3000"
	}

	clients := make(map[string]*client)
	clientsRWMutex := sync.RWMutex{}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		// Extract file name from URL
		url := r.URL.String()
		index := strings.LastIndex(url, "/"+prefix+"/")
		if index < 0 {
			http.NotFound(w, r)
			return
		}

		fileName := url[index+len(prefix)+2:]

		if r.Method == "POST" {
			// Upload

			user, pass, ok := r.BasicAuth()
			if !ok || user != validUserName || pass != validPassword {
				http.Error(w, "Invalid Credentials", http.StatusUnauthorized)
				return
			}

			// Generate a unique file ID
			b := make([]byte, 36)
			source := rand.NewSource(time.Now().UnixNano())
			rng := rand.New(source)
			n, err := rng.Read(b)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(err.Error()))
				return
			}
			encodedLength := base64.StdEncoding.EncodedLen(n)
			buffer := bufPool.Get().(*[]byte)
			defer bufPool.Put(buffer)
			base64.URLEncoding.Encode(*buffer, b)

			fileID := string((*buffer)[:encodedLength])

			// If client name already exists, error.
			clientsRWMutex.RLock()
			_, ok = clients[fileID]
			clientsRWMutex.RUnlock()

			if ok {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("File already exists. Choose a different name."))
				return
			}

			// Create a new client.
			clientsRWMutex.Lock()
			receiverCh := make(chan bool)
			newClient := &client{
				clientConnected: receiverCh,
				fileName:        fileName,
			}
			clients[fileID] = newClient

			defer func() {
				// Remove client.
				clientsRWMutex.Lock()
				delete(clients, fileID)
				clientsRWMutex.Unlock()
			}()
			clientsRWMutex.Unlock()

			// NOTE: Cannot do Flush() since Go closes the request body and we get an error (http: invalid Read on closed Body).
			// The alternative is to hijack the http connection or use HTTP2 with TLS (h2c requires draining the full request body upfront).
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
				return
			}
			conn, bufrw, err := hj.Hijack()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer conn.Close()
			w = &responseLogWriter{body: bufrw.Writer, header: make(http.Header)}
			w.Write([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\n\r\nTo download the file, curl -o %s %s/%s/%s\n", fileName, downloadBaseUrl, prefix, fileID)))
			bufrw.Flush()

			// Wait for a client to stream the file to.
			select {
			case <-receiverCh:
				w.Write([]byte("Client connected.\n"))
				bufrw.Writer.Flush()

			case <-r.Context().Done():
				w.Write([]byte("Request disconnected.\n"))
				bufrw.Writer.Flush()
				return

			case <-time.After(120 * time.Second):
				w.Write([]byte("Timed out. No client connected in 120 seconds.\n"))
				bufrw.Writer.Flush()
				return
			}

			defer func() {
				newClient.downloadCompleted <- true
			}()

			// Copy the request body to client
			(*newClient.receiver).Header().Add("content-disposition", "attachment; filename=\""+fileName+"\"")
			_, err = io.CopyBuffer(*newClient.receiver, io.LimitReader(bufrw, r.ContentLength), *buffer)
			if err != nil {
				w.Write([]byte(err.Error()))
				bufrw.Writer.Flush()
				return
			}

			w.Write([]byte(fmt.Sprintf("%s was transferred successfully.\n", fileName)))
			bufrw.Writer.Flush()
		} else if r.Method == "GET" {

			// If client does not exist error.
			clientsRWMutex.RLock()
			client, ok := clients[fileName] // Name here is the file ID.
			clientsRWMutex.RUnlock()
			if ok {
				if !client.receiving {
					clientsRWMutex.Lock()
					client.receiver = &w
					client.receiving = true
					client.downloadCompleted = make(chan bool)
					clientsRWMutex.Unlock()
					client.clientConnected <- true
				} else {
					http.Error(w, "File already being received by another client.\n", http.StatusBadRequest)
					return
				}
			} else {
				http.NotFound(w, r)
				return
			}
			// Wait for transfer.
			<-client.downloadCompleted
		}
	})

	server := &http.Server{
		Addr: ":" + port,
	}

	go func() {
		// Service connections.
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error listening on server. %s", err)
		}
	}()
	log.Printf("Server started after %d ms.\n", time.Since(startTime)/time.Millisecond)

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 10 seconds.
	quit := make(chan os.Signal)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
	time.Sleep(3 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Error shutting down server. %s", err)
	}
	log.Println("Server exiting...")
}

const (
	noWritten     = -1
	defaultStatus = http.StatusOK
)

type responseLogWriter struct {
	http.ResponseWriter
	body   *bufio.Writer
	header http.Header
	status int
	size   int
}

func (w responseLogWriter) WriteString(s string) (n int, err error) {
	return w.body.WriteString(s)
}

func (w *responseLogWriter) WriteHeader(code int) {
	if code > 0 && w.status != code {
		w.status = code
	}
}

func (w *responseLogWriter) WriteHeaderNow() {
	if !w.Written() {
		w.size = 0
		w.ResponseWriter.WriteHeader(w.status)
	}
}

func (w *responseLogWriter) Write(data []byte) (n int, err error) {
	w.WriteHeaderNow()
	n, err = w.body.Write(data)
	w.size += n
	return
}

func (c *responseLogWriter) Status() int {
	return c.status
}

func (c *responseLogWriter) Size() int {
	return c.size
}

func (c *responseLogWriter) Written() bool {
	return c.size != noWritten
}

func (w *responseLogWriter) Flush() {
	w.WriteHeaderNow()
	w.body.Flush()
}

func (w responseLogWriter) Header() http.Header {
	return w.header
}
