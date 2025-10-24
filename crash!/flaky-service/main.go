// flaky-service/main.go

package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	http.HandleFunc("/process", func(w http.ResponseWriter, r *http.Request) {
		// Simulate random failures and slow responses
		randomValue := rand.Float32()

		if randomValue < 0.3 {
			// 30% chance: Timeout (very slow)
			fmt.Println("Simulating a timeout...")
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Service overloaded!")
			return
		}

		if randomValue < 0.5 {
			// 20% chance: Quick failure
			fmt.Println("❌ Simulating quick failure...")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Payment processor error!")
			return
		}

		// 50% chance: Success
		fmt.Println("✅ Payment processed successfully")
		fmt.Fprintf(w, "Payment successful!")
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Flaky service is running")
	})

	fmt.Println("💳 Flaky Payment Service starting on :8081")
	http.ListenAndServe(":8081", nil)
}
