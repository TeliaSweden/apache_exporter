package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
)

const (
	namespace = "apache" // For Prometheus metrics.
)

var (
	listeningAddress = flag.String("telemetry.address", ":9117", "Address on which to expose metrics.")
	metricsEndpoint  = flag.String("telemetry.endpoint", "/metrics", "Path under which to expose metrics.")
	scrapeURI        = flag.String("scrape_uri", "http://localhost/server-status/?auto", "URI to apache stub status page.")
	insecure         = flag.Bool("insecure", false, "Ignore server certificate if using https.")
	showVersion      = flag.Bool("version", false, "Print version information.")
)

type Exporter struct {
	URI    string
	mutex  sync.Mutex
	client *http.Client

	up             *prometheus.Desc
	scrapeFailures prometheus.Counter
	accessesTotal  *prometheus.Desc
	kBytesTotal    *prometheus.Desc
	durationTotal  *prometheus.Desc
	cpu            *prometheus.GaugeVec
	uptime         *prometheus.Desc
	workers        *prometheus.GaugeVec
	scoreboard     *prometheus.GaugeVec
	connections    *prometheus.GaugeVec
}

func NewExporter(uri string) *Exporter {
	return &Exporter{
		URI: uri,
		up: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "up"),
			"Could the apache server be reached",
			[]string{"version", "mpm"},
			nil),
		scrapeFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_scrape_failures_total",
			Help:      "Number of errors while scraping apache.",
		}),
		accessesTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "accesses_total"),
			"Current total apache accesses (*)",
			nil,
			nil),
		kBytesTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "sent_kilobytes_total"),
			"Current total kbytes sent (*)",
			nil,
			nil),
		durationTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "duration_total"),
			"Current cumulated response duration time in milliseconds (*)",
			nil,
			nil),
		cpu: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "cpu",
			Help:      "Apache CPU usage (*)",
		},
			[]string{"usage"},
		),
		uptime: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "uptime_seconds_total"),
			"Current uptime in seconds (*)",
			nil,
			nil),
		workers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "workers",
			Help:      "Apache worker statuses",
		},
			[]string{"state"},
		),
		scoreboard: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "scoreboard",
			Help:      "Apache scoreboard statuses",
		},
			[]string{"state"},
		),
		connections: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "connections",
			Help:      "Apache connection statuses",
		},
			[]string{"state"},
		),
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure},
			},
		},
	}
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.up
	ch <- e.accessesTotal
	ch <- e.kBytesTotal
	ch <- e.durationTotal
	ch <- e.uptime
	e.cpu.Describe(ch)
	e.scrapeFailures.Describe(ch)
	e.workers.Describe(ch)
	e.scoreboard.Describe(ch)
	e.connections.Describe(ch)
}

// Split colon separated string into two fields
func splitkv(s string) (string, string) {

	if len(s) == 0 {
		return s, s
	}

	slice := strings.SplitN(s, ":", 2)

	if len(slice) == 1 {
		return slice[0], ""
	}

	return strings.TrimSpace(slice[0]), strings.TrimSpace(slice[1])
}

var scoreboardLabelMap = map[string]string{
	"_": "idle",
	"S": "startup",
	"R": "read",
	"W": "reply",
	"K": "keepalive",
	"D": "dns",
	"C": "closing",
	"L": "logging",
	"G": "graceful_stop",
	"I": "idle_cleanup",
	".": "open_slot",
}

func (e *Exporter) updateScoreboard(scoreboard string) {
	e.scoreboard.Reset()
	for _, v := range scoreboardLabelMap {
		e.scoreboard.WithLabelValues(v)
	}

	for _, worker_status := range scoreboard {
		s := string(worker_status)
		label, ok := scoreboardLabelMap[s]
		if !ok {
			label = s
		}
		e.scoreboard.WithLabelValues(label).Inc()
	}
}

func (e *Exporter) collect(ch chan<- prometheus.Metric) error {
	var apacheVersion, apacheMpm string

	resp, err := e.client.Get(e.URI)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, 0, apacheVersion, apacheMpm)
		return fmt.Errorf("Error scraping apache: %v", err)
	}

	data, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		if err != nil {
			data = []byte(err.Error())
		}
		ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, 1, apacheVersion, apacheMpm)
		return fmt.Errorf("Status %s (%d): %s", resp.Status, resp.StatusCode, data)
	}

	lines := strings.Split(string(data), "\n")

	connectionInfo := false

	for _, l := range lines {
		key, v := splitkv(l)
		if err != nil {
			continue
		}

		switch {
		case key == "ServerVersion":
			apacheVersion = v
		case key == "ServerMPM":
			apacheMpm = v
		case key == "Total Accesses":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			ch <- prometheus.MustNewConstMetric(e.accessesTotal, prometheus.CounterValue, val)
		case key == "Total kBytes":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			ch <- prometheus.MustNewConstMetric(e.kBytesTotal, prometheus.CounterValue, val)
		case key == "Total Duration":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			ch <- prometheus.MustNewConstMetric(e.durationTotal, prometheus.CounterValue, val)
		case key == "CPUUser":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.cpu.WithLabelValues("user").Set(val)
		case key == "CPUSystem":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.cpu.WithLabelValues("system").Set(val)
		case key == "CPUChildrenUser":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.cpu.WithLabelValues("children_user").Set(val)
		case key == "CPUChildrenSystem":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.cpu.WithLabelValues("children_system").Set(val)
		case key == "CPULoad":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.cpu.WithLabelValues("load").Set(val)
		case key == "Uptime":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			ch <- prometheus.MustNewConstMetric(e.uptime, prometheus.CounterValue, val)
		case key == "BusyWorkers":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.workers.WithLabelValues("busy").Set(val)
		case key == "IdleWorkers":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.workers.WithLabelValues("idle").Set(val)
		case key == "Scoreboard":
			e.updateScoreboard(v)
			e.scoreboard.Collect(ch)
		case key == "ConnsTotal":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.connections.WithLabelValues("total").Set(val)
			connectionInfo = true
		case key == "ConnsAsyncWriting":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}

			e.connections.WithLabelValues("writing").Set(val)
			connectionInfo = true
		case key == "ConnsAsyncKeepAlive":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}
			e.connections.WithLabelValues("keepalive").Set(val)
			connectionInfo = true
		case key == "ConnsAsyncClosing":
			val, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}
			e.connections.WithLabelValues("closing").Set(val)
			connectionInfo = true
		}
	}

	ch <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, 1, apacheVersion, apacheMpm)

	e.cpu.Collect(ch)
	e.workers.Collect(ch)
	if connectionInfo {
		e.connections.Collect(ch)
	}

	return nil
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()
	if err := e.collect(ch); err != nil {
		log.Errorf("Error scraping apache: %s", err)
		e.scrapeFailures.Inc()
		e.scrapeFailures.Collect(ch)
	}
	return
}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, version.Print("apache_exporter"))
		os.Exit(0)
	}
	exporter := NewExporter(*scrapeURI)
	prometheus.MustRegister(exporter)
	prometheus.MustRegister(version.NewCollector("apache_exporter"))

	log.Infoln("Starting apache_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())
	log.Infof("Starting Server: %s", *listeningAddress)

	http.Handle(*metricsEndpoint, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			 <head><title>Apache Exporter</title></head>
			 <body>
			 <h1>Apache Exporter</h1>
			 <p><a href='` + *metricsEndpoint + `'>Metrics</a></p>
			 </body>
			 </html>`))
	})
	log.Fatal(http.ListenAndServe(*listeningAddress, nil))
}
