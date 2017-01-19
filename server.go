package bla

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ini "gopkg.in/ini.v1"
)

var (
	logPool = sync.Pool{New: func() interface{} { return &LogWriter{nil, 200} }}

	tlsCert *tls.Certificate
	server  *http.Server
	cfg     *ServerConfig

	httpRequestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "http",
			Subsystem: "requests",
			Name:      "total",
			Help:      "The total number of http request",
		},
		[]string{"handler"})

	httpRequestDurationSeconds = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Namespace: "http",
			Subsystem: "request",
			Name:      "duration_seconds",
			Help:      "The request duration distribution",
		})
)

func listenMetric(addr string) {
	log.Printf("prometheus metric at %s/%s", addr, "metrics")
	prometheus.MustRegister(httpRequestCount)
	prometheus.MustRegister(httpRequestDurationSeconds)
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(addr, nil)

}

// Config ---------------------

type ServerConfig struct {
	Certfile         string
	Keyfile          string
	Listen           string
	MetricListenAddr string
	AccessLogPath    string
	ListenLEAddr     string
	LEDir            string
}

func ListenAndServe(cfgPath string) {
	raw, err := ini.Load(cfgPath)
	if err != nil {
		log.Fatal(err)
	}

	cfg = &ServerConfig{
		"", "",
		":8080",
		"",
		"access.log",
		"",
		"",
	}

	raw.MapTo(cfg)

	log.Printf("pid:%d", os.Getpid())

	if cfg.MetricListenAddr != "" {
		go listenMetric(cfg.MetricListenAddr)
	}

	log.Printf("Server:%v", cfg)

	h := NewHandler(cfgPath)

	lh := logTimeAndStatus(cfg, h)
	server = &http.Server{Addr: cfg.Listen, Handler: lh}

	if cfg.Certfile != "" && cfg.Keyfile != "" {
		// for higher score in ssllab
		server.TLSConfig = &tls.Config{}
		server.TLSConfig.GetCertificate = getCertificate
		log.Printf("TLS:%s, %s", cfg.Certfile, cfg.Keyfile)
		LoadCertificate()
		log.Fatal(server.ListenAndServeTLS(cfg.Certfile, cfg.Keyfile))
	}
	server.ListenAndServe()
}

type LogWriter struct {
	http.ResponseWriter
	statusCode int
}

func (l *LogWriter) WriteHeader(i int) {
	l.statusCode = i
	l.ResponseWriter.WriteHeader(i)
}
func logTimeAndStatus(cfg *ServerConfig, handler http.Handler) http.Handler {

	var (
		writer io.Writer
		err    error
	)

	if cfg.AccessLogPath != "" {
		var file *os.File
		file, err = os.OpenFile(cfg.AccessLogPath,
			os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Fatal(err)
		}
		writer = file
		log.Printf("Access Log to file: %s", cfg.AccessLogPath)
		file.Seek(0, os.SEEK_END)
	} else {
		writer = os.Stdout
	}

	accessLogger := log.New(writer, "", log.LstdFlags)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		writer := logPool.Get().(*LogWriter)
		writer.ResponseWriter = w
		writer.statusCode = 200

		if cfg.Certfile != "" {
			writer.ResponseWriter.Header().Add("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}
		handler.ServeHTTP(writer, r)

		delta := time.Now().Sub(start)
		accessLogger.Printf("%s %s %s %s %d",
			r.RemoteAddr, r.Method, r.URL.Path,
			delta, writer.statusCode)

		httpRequestDurationSeconds.Observe(delta.Seconds())

		logPool.Put(writer)
	})
}

func LoadCertificate() {

	log.Println("Loading new certs")
	cert, err := tls.LoadX509KeyPair(cfg.Certfile, cfg.Keyfile)
	if err != nil {
		log.Println("load cert failed keep old", err)
		return
	}
	tlsCert = &cert
}

func getCertificate(ch *tls.ClientHelloInfo) (cert *tls.Certificate, err error) {
	return tlsCert, nil
}
