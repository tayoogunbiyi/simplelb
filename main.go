package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Attempts int = iota
	Retry
	ConfigFileName = "config.json"
	RoundRobin = "round_robin"
)

var BalancingAlgorithmMap = make(map[string]bool)
var serverPool ServerPool

func init(){
	balancingAlgorithms := []string{RoundRobin,}
	for _,balancingAlgorithm := range balancingAlgorithms {
		BalancingAlgorithmMap[balancingAlgorithm] = true
	}
}

type Config struct {
	BackendURLs        []string `json:"backendURLs"`
	BalancingAlgorithm string `json:"balancingAlgorithm"`
	Port int `json:"port" validate:"required"`
}

func ParseConfiguration(filename string) (*Config, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	byteData, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var config Config
	err = json.Unmarshal(byteData, &config)

	return &config, err
}

// Backend holds the data about a server
type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

// SetAlive for this backend
func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

// IsAlive returns true when backend is alive
func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.Alive
	b.mux.RUnlock()
	return
}

// ServerPool holds information about reachable backends
type ServerPool struct {
	backends []*Backend
	BackendSelector
	balancingAlgorithm string
}

func NewServerPool(backends []*Backend,balancingAlgorithm string) ServerPool{
	_, ok := BalancingAlgorithmMap[balancingAlgorithm]
	if !ok {
		log.Printf("unknown balancing algorithm '%s'. using default round robin \n",balancingAlgorithm)
		balancingAlgorithm = RoundRobin
	}else{
		log.Printf("using %s as the balancing algorithm",balancingAlgorithm)
	}

	var bs BackendSelector
	switch balancingAlgorithm {
	case RoundRobin:
		bs = RoundRobinSelector{
			backends: &backends,
		}
	default:
		bs = RoundRobinSelector{
			backends: &backends,
		}
	}

	return ServerPool{
		backends:           backends,
		BackendSelector:     bs,
	}
}


type BackendSelector interface {
	NextBackend() *Backend
}

type RoundRobinSelector struct {
	backends *[]*Backend
	current uint64
}

func (r RoundRobinSelector) nextIndex() int{
	return int(atomic.AddUint64(&r.current, uint64(1)) % uint64(len(*r.backends)))
}

func (r RoundRobinSelector) NextBackend() *Backend{
	return nil
}

// AddBackend to the server pool
func (s *ServerPool) AddBackend(backend *Backend) {
	s.backends = append(s.backends, backend)
}

// MarkBackendStatus changes a status of a backend
func (s *ServerPool) MarkBackendStatus(backendUrl *url.URL, alive bool) {
	for _, b := range s.backends {
		if b.URL.String() == backendUrl.String() {
			b.SetAlive(alive)
			break
		}
	}
}

// HealthCheck pings the backends and update the status
func (s *ServerPool) HealthCheck() {
	for _, b := range s.backends {
		status := "up"
		alive := isBackendAlive(b.URL)
		b.SetAlive(alive)
		if !alive {
			status = "down"
		}
		log.Printf("%s [%s]\n", b.URL, status)
	}
}

// GetAttemptsFromContext returns the attempts for request
func GetAttemptsFromContext(r *http.Request) int {
	if attempts, ok := r.Context().Value(Attempts).(int); ok {
		return attempts
	}
	return 1
}

// GetAttemptsFromContext returns the attempts for request
func GetRetryFromContext(r *http.Request) int {
	if retry, ok := r.Context().Value(Retry).(int); ok {
		return retry
	}
	return 0
}

// lb load balances the incoming request
func lb(w http.ResponseWriter, r *http.Request) {
	attempts := GetAttemptsFromContext(r)
	if attempts > 3 {
		log.Printf("%s(%s) Max attempts reached, terminating\n", r.RemoteAddr, r.URL.Path)
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	backend := serverPool.NextBackend()
	if backend != nil {
		backend.ReverseProxy.ServeHTTP(w, r)
		return
	}
	http.Error(w, "Service not available", http.StatusServiceUnavailable)
}

// isAlive checks whether a backend is Alive by establishing a TCP connection
func isBackendAlive(u *url.URL) bool {
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		log.Println("Site unreachable, error: ", err)
		return false
	}
	_ = conn.Close()
	return true
}

// healthCheck runs a routine for check status of the backends every 2 mins
func healthCheck() {
	t := time.NewTicker(time.Minute * 2)
	for {
		select {
		case <-t.C:
			log.Println("Starting health check...")
			serverPool.HealthCheck()
			log.Println("Health check completed")
		}
	}
}

func main() {
	config, err := ParseConfiguration(ConfigFileName)
	if err != nil{
		log.Fatalf("unable to load configuration. err: %s",err)
	}

	if len(config.BackendURLs) == 0 {
		log.Fatal("Please provide one or more backends to load balance")
	}

	var backends []*Backend
	// parse servers
	for _, backendURL := range config.BackendURLs {
		parsedBackendURL, err := url.Parse(backendURL)
		if err != nil {
			log.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(parsedBackendURL)
		proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, e error) {
			log.Printf("[%s] %s\n", parsedBackendURL.Host, e.Error())
			retries := GetRetryFromContext(request)
			if retries < 3 {
				select {
				case <-time.After(10 * time.Millisecond):
					ctx := context.WithValue(request.Context(), Retry, retries+1)
					proxy.ServeHTTP(writer, request.WithContext(ctx))
				}
				return
			}

			// after 3 retries, mark this backend as down
			serverPool.MarkBackendStatus(parsedBackendURL, false)

			// if the same request routing for few attempts with different backends, increase the count
			attempts := GetAttemptsFromContext(request)
			log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attempts)
			ctx := context.WithValue(request.Context(), Attempts, attempts+1)
			lb(writer, request.WithContext(ctx))
		}

		backends = append(backends,&Backend{
			URL:          parsedBackendURL,
			Alive:        true,
			ReverseProxy: proxy,
		})
		log.Printf("Configured server: %s\n", parsedBackendURL)
	}

	serverPool = NewServerPool(backends,config.BalancingAlgorithm)
	// create http server
	server := http.Server{
		Addr:    fmt.Sprintf(":%d", config.Port),
		Handler: http.HandlerFunc(lb),
	}

	// start health checking
	go healthCheck()

	log.Printf("Load Balancer started at :%d\n", config.Port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
