// api-service/main.go - BROKEN VERSION (No Circuit Breaker)
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
)

type CheckoutRequest struct {
	Item  string  `json:"item"`
	Price float64 `json:"price"`
}

type Metrics struct {
	TotalRequests      int
	SuccessfulRequests int
	FailedRequests     int
	TotalLatency       time.Duration
	LatencyHistory     []time.Duration
	mu                 sync.Mutex
}

var metrics = &Metrics{}

func main() {
	// Serve static frontend
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/index.html")
	})

	// Checkout endpoint
	http.HandleFunc("/api/checkout", handleCheckout)

	// Metrics endpoint
	http.HandleFunc("/metrics", handleMetrics)

	// Health endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("🟢 System Operational (No Protection)"))
	})

	log.Println("🚀 Store API running on :8080 WITHOUT CIRCUIT BREAKER")
	log.Println("⚠️  WARNING: No failure protection - timeouts will block!")
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

	// Call payment service directly - NO CIRCUIT BREAKER!
	resp, err := callPaymentService(flakyURL)

	duration := time.Since(start)
	updateMetrics(err, duration)

	// Handle failures
	if err != nil {
		log.Printf("❌ FAILURE: %v (%.0fms) - NO PROTECTION!", err, duration.Seconds()*1000)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "Payment processing failed",
			"root_cause": err.Error(),
			"latency":    duration.String(),
		})
		return
	}

	// Success case
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
func updateMetrics(err error, latency time.Duration) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	metrics.TotalRequests++
	metrics.TotalLatency += latency
	metrics.LatencyHistory = append(metrics.LatencyHistory, latency)

	if err != nil {
		metrics.FailedRequests++
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

	// Create structured metrics response
	response := struct {
		SystemStatus  string  `json:"system_status"`
		Version       string  `json:"version"`
		TotalRequests int     `json:"total_requests"`
		SuccessCount  int     `json:"success_count"`
		FailureCount  int     `json:"failure_count"`
		SuccessRate   float64 `json:"success_rate"`
		ErrorRate     float64 `json:"error_rate"`
		AvgLatency    string  `json:"avg_latency"`
		MedianLatency string  `json:"median_latency"`
		P95Latency    string  `json:"p95_latency"`
		P99Latency    string  `json:"p99_latency"`
		Warning       string  `json:"warning"`
	}{
		SystemStatus:  "vulnerable",
		Version:       "BROKEN - No Circuit Breaker",
		TotalRequests: metrics.TotalRequests,
		SuccessCount:  metrics.SuccessfulRequests,
		FailureCount:  metrics.FailedRequests,
		SuccessRate:   successRate,
		ErrorRate:     errorRate,
		AvgLatency:    avgLatency.String(),
		MedianLatency: p50.String(),
		P95Latency:    p95.String(),
		P99Latency:    p99.String(),
		Warning:       "⚠️ All failures wait for full timeout (3000ms)!",
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
