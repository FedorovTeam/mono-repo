package middleware

import "net/http"

// CORSMiddleware добавляет CORS-заголовки и обрабатывает preflight-запросы.
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	// маленькая функция для проверки origin'а
	isAllowed := func(origin string) bool {
		if origin == "" {
			return false
		}
		for _, o := range allowedOrigins {
			if o == "*" || o == origin {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if isAllowed(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "false")
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Correlation-ID, Authorization")
			}

			// Preflight — отвечаем сразу и дальше не идём
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
