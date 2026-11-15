// Command loadtest fires a burst of SOS requests (an "SOS flood") at the API
// and reports status distribution and latency, so the < 300 ms requirement
// can be verified on any machine:
//
//	go run ./cmd/loadtest -url http://localhost:8080 -n 600 -c 100 -spoof
//
// -spoof sets a random X-Forwarded-For per request to imitate a botnet
// (only honoured by the server when TRUST_PROXY=true).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080", "base URL")
	n := flag.Int("n", 600, "total requests")
	c := flag.Int("c", 100, "concurrency")
	spoof := flag.Bool("spoof", false, "random X-Forwarded-For per request (botnet)")
	same := flag.Bool("same-spot", false, "all requests from the same point/type (tests the duplicate filter)")
	flag.Parse()

	types := []string{"fire", "collapse", "flood", "medical"}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: *c, MaxConnsPerHost: 0}}
	var wg sync.WaitGroup
	jobs := make(chan int)
	var mu sync.Mutex
	lat := make([]float64, 0, *n)
	status := map[int]int{}
	var dup, suspect, created int64

	start := time.Now()
	for w := 0; w < *c; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(time.Now().UnixNano() ^ int64(rand.Int())))
			for i := range jobs {
				body := map[string]any{"citizen_id": fmt.Sprintf("bot-%d-%d", i, rnd.Intn(1e6)), "type": types[i%4],
					"lat": 40.3870 + rnd.Float64()*0.05, "lng": 49.8300 + rnd.Float64()*0.08}
				if *same {
					body["type"], body["lat"], body["lng"] = "fire", 40.3870, 49.8300
				}
				b, _ := json.Marshal(body)
				req, _ := http.NewRequest(http.MethodPost, *url+"/api/v1/sos", bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				if *spoof {
					req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.%d", rnd.Intn(255), rnd.Intn(255), rnd.Intn(255)))
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				ms := float64(time.Since(t0).Microseconds()) / 1000
				code := 0
				if err == nil {
					code = resp.StatusCode
					var out struct {
						Duplicate bool `json:"duplicate"`
						SOS       struct {
							Suspect bool `json:"suspect"`
						} `json:"sos"`
					}
					_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
					resp.Body.Close()
					switch {
					case out.Duplicate:
						atomic.AddInt64(&dup, 1)
					case code == 201 && out.SOS.Suspect:
						atomic.AddInt64(&suspect, 1)
					case code == 201:
						atomic.AddInt64(&created, 1)
					}
				}
				mu.Lock()
				lat = append(lat, ms)
				status[code]++
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < *n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	total := time.Since(start)

	sort.Float64s(lat)
	var sum float64
	for _, v := range lat {
		sum += v
	}
	p := func(q float64) float64 { return lat[int(float64(len(lat)-1)*q)] }
	fmt.Printf("requests: %d  concurrency: %d  wall: %.2fs  throughput: %.0f req/s\n", *n, *c, total.Seconds(), float64(*n)/total.Seconds())
	fmt.Printf("latency  avg: %.1f ms  p50: %.1f ms  p95: %.1f ms  p99: %.1f ms  max: %.1f ms\n", sum/float64(len(lat)), p(.5), p(.95), p(.99), lat[len(lat)-1])
	keys := make([]int, 0, len(status))
	for k := range status {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	fmt.Print("status:  ")
	for _, k := range keys {
		fmt.Printf("%d=%d  ", k, status[k])
	}
	fmt.Printf("\ncreated: %d  flagged suspect: %d  duplicates dropped: %d  rate-limited(429): %d\n", created, suspect, dup, status[429])
	if avg := sum / float64(len(lat)); avg < 300 {
		fmt.Printf("RESULT: OK — average latency %.1f ms < 300 ms\n", avg)
	} else {
		fmt.Printf("RESULT: FAIL — average latency %.1f ms >= 300 ms\n", avg)
	}
}
