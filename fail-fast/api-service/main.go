// api-service/main.go
// critical code path for handling checkout requests
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sony/gobreaker"
)

type CheckoutRequest struct {
	Item  string  `json:"item"`
	Price float64 `json:"price"`
}

type Metrics struct {
	TotalRequests      int
	SuccessfulRequests int
	FailedRequests     int
	CircuitOpenRejects int
	TotalLatency       time.Duration
	LatencyHistory     []time.Duration
	mu                 sync.Mutex
}

var (
	metrics = &Metrics{}
	cb      *gobreaker.CircuitBreaker
)

func main() {
	// Configure Circuit Breaker with more sensitive settings
	cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "payment-service",
		MaxRequests: 2,                // Fewer requests in half-open state
		Interval:    20 * time.Second, // Shorter tracking window
		Timeout:     10 * time.Second, // Faster recovery attempts
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Trip on either 3 consecutive failures OR 50% failure rate
			if counts.ConsecutiveFailures >= 3 {
				return true
			}
			if counts.Requests >= 5 {
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return failureRatio >= 0.5
			}
			return false
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			log.Printf("🔌 STATE CHANGE: %s → %s", from, to)
			if to == gobreaker.StateHalfOpen {
				log.Println("⚠️ Attempting recovery in half-open state")
			}
		},
	})

	// Serve static frontend
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/index.html")
	})

	// Checkout endpoint
	http.HandleFunc("/api/checkout", handleCheckout)

	// Enhanced metrics endpoint
	http.HandleFunc("/metrics", handleMetrics)

	// Circuit breaker state endpoint with counts
	http.HandleFunc("/circuit-state", func(w http.ResponseWriter, r *http.Request) {
		currentCounts := cb.Counts()
		stateInfo := struct {
			State  gobreaker.State
			Counts gobreaker.Counts
		}{
			State:  cb.State(),
			Counts: currentCounts,
		}
		json.NewEncoder(w).Encode(stateInfo)
	})

	// System health endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("🟢 System Operational"))
	})

	log.Println("🚀 Store API running on :8080 WITH ENHANCED CIRCUIT BREAKER")
	log.Println("📍 Open http://localhost:8080 in your browser")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleCheckout(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var req CheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid request format"))
		return
	}

	flakyURL := getFlakyServiceURL()

	// Execute via circuit breaker
	result, err := cb.Execute(func() (interface{}, error) {
		return callPaymentService(flakyURL)
	})

	duration := time.Since(start)
	updateMetrics(err, duration, cb.State())

	// Handle circuit breaker rejection
	if err == gobreaker.ErrOpenState {
		log.Printf("⚡ FAST FAIL: Request rejected (%.0fms) - Circuit OPEN", duration.Seconds()*1000)
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "Service unavailable",
			"advice":  "Try again shortly",
			"state":   "open",
			"latency": duration.String(),
		})
		return
	}

	// Handle service failures
	if err != nil {
		log.Printf("❌ FAILURE: %v (%.0fms)", err, duration.Seconds()*1000)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "Payment processing failed",
			"root_cause": err.Error(),
			"latency":    duration.String(),
		})
		return
	}

	// Success case
	resp := result.(*http.Response)
	defer resp.Body.Close()

	log.Printf("✅ SUCCESS: %s for $%.2f (%s)", req.Item, req.Price, duration)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "confirmed",
		"item":    req.Item,
		"charged": fmt.Sprintf("%.2f", req.Price),
		"latency": duration.String(),
	})
}

// Helper function for service calls
func callPaymentService(baseURL string) (*http.Response, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/process")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service error (%d: %s)", resp.StatusCode, resp.Status)
	}
	return resp, nil
}

// Centralized metrics update with thread safety
func updateMetrics(err error, latency time.Duration, state gobreaker.State) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	metrics.TotalRequests++
	metrics.TotalLatency += latency
	metrics.LatencyHistory = append(metrics.LatencyHistory, latency) // ← Add this

	if err != nil {
		metrics.FailedRequests++
		if state == gobreaker.StateOpen {
			metrics.CircuitOpenRejects++
		}
	} else {
		metrics.SuccessfulRequests++
	}
}

// Get flaky service URL with default
func getFlakyServiceURL() string {
	if url := os.Getenv("FLAKY_SERVICE_URL"); url != "" {
		return url
	}
	return "http://flaky-service:8081"
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	// Calculate metrics
	avgLatency := time.Duration(0)
	errorRate := 0.0
	successRate := 0.0

	if metrics.TotalRequests > 0 {
		avgLatency = metrics.TotalLatency / time.Duration(metrics.TotalRequests)
		successRate = float64(metrics.SuccessfulRequests) / float64(metrics.TotalRequests) * 100
		errorRate = 100 - successRate
	}

	// Calculate percentiles
	p50 := calculatePercentile(metrics.LatencyHistory, 0.50)
	p95 := calculatePercentile(metrics.LatencyHistory, 0.95)
	p99 := calculatePercentile(metrics.LatencyHistory, 0.99)

	currentCounts := cb.Counts()
	currentState := cb.State()

	// Create structured metrics response
	response := struct {
		SystemStatus  string           `json:"system_status"`
		CircuitState  gobreaker.State  `json:"circuit_state"`
		CircuitCounts gobreaker.Counts `json:"circuit_counts"`
		TotalRequests int              `json:"total_requests"`
		SuccessCount  int              `json:"success_count"`
		FailureCount  int              `json:"failure_count"`
		FastFails     int              `json:"fast_fails"`
		SuccessRate   float64          `json:"success_rate"`
		ErrorRate     float64          `json:"error_rate"`
		AvgLatency    string           `json:"avg_latency"`
		MedianLatency string           `json:"median_latency"`
		P95Latency    string           `json:"p95_latency"`
		P99Latency    string           `json:"p99_latency"`
	}{
		SystemStatus:  "operational",
		CircuitState:  currentState,
		CircuitCounts: currentCounts,
		TotalRequests: metrics.TotalRequests,
		SuccessCount:  metrics.SuccessfulRequests,
		FailureCount:  metrics.FailedRequests,
		FastFails:     metrics.CircuitOpenRejects,
		SuccessRate:   successRate,
		ErrorRate:     errorRate,
		AvgLatency:    avgLatency.String(),
		MedianLatency: p50.String(),
		P95Latency:    p95.String(),
		P99Latency:    p99.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func calculatePercentile(latencies []time.Duration, percentile float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}

	// Make a copy to avoid modifying original
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	index := int(float64(len(sorted)) * percentile)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
