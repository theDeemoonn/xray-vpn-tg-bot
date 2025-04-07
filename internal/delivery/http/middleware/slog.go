package middleware

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// SlogMiddleware создает middleware для логирования HTTP запросов с использованием slog.
func SlogMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := NewResponseWriterWrapper(w) // Используем обертку для захвата статуса

			defer func() {
				duration := time.Since(start)
				logger.Info("Request completed",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("remote_addr", r.RemoteAddr),
					slog.Int("status", ww.Status()), // Получаем статус из обертки
					slog.Duration("duration", duration),
					// Добавьте другие поля, если нужно, например, User-Agent
					// slog.String("user_agent", r.UserAgent()),
					// Можно добавить request_id из контекста, если он там есть
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// ResponseWriterWrapper обертка для http.ResponseWriter, чтобы захватить код статуса.
type ResponseWriterWrapper struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func NewResponseWriterWrapper(w http.ResponseWriter) *ResponseWriterWrapper {
	// По умолчанию статус 200 OK, если WriteHeader не вызывался явно
	return &ResponseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}
}

func (rw *ResponseWriterWrapper) WriteHeader(statusCode int) {
	if rw.wroteHeader {
		return
	}
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
	rw.wroteHeader = true
}

// Write вызывает WriteHeader с http.StatusOK если он еще не был вызван.
func (rw *ResponseWriterWrapper) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Status возвращает записанный код статуса.
func (rw *ResponseWriterWrapper) Status() int {
	return rw.statusCode
}

// Hijack для поддержки интерфейса http.Hijacker (например, для WebSocket)
func (rw *ResponseWriterWrapper) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("http.Hijacker interface is not supported")
	}
	return h.Hijack()
}

// Flush для поддержки интерфейса http.Flusher
func (rw *ResponseWriterWrapper) Flush() {
	fl, ok := rw.ResponseWriter.(http.Flusher)
	if ok {
		fl.Flush()
	}
}
