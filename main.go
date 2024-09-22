package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	power   *prometheus.GaugeVec
	voltage *prometheus.GaugeVec
	current *prometheus.GaugeVec
	energy  *prometheus.GaugeVec
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx); !errors.Is(err, context.Canceled) {
		panic(err)
	}
}
func run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)

	power = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "shelly",
		Subsystem: "switch",
		Name:      "power",
	}, []string{"id"})
	voltage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "shelly",
		Subsystem: "switch",
		Name:      "voltage",
	}, []string{"id"})
	current = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "shelly",
		Subsystem: "switch",
		Name:      "current",
	}, []string{"id"})
	energy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "shelly",
		Subsystem: "switch",
		Name:      "energy",
	}, []string{"id"})

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := collectMetrics(); err != nil {
					cancel(fmt.Errorf("error collecting metrics: %w", err))
				}
			}
		}
	}()

	server := http.Server{
		Addr:    net.JoinHostPort("0.0.0.0", "2002"),
		Handler: promhttp.Handler(), // all routes
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	log.Printf("listening on %s\n", server.Addr)

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("error listening on %s: %w", server.Addr, err)
	}
	return context.Cause(ctx)
}

func collectMetrics() error {
	plug_addresses := os.Getenv("SHELLY_PLUG_ADDRESS")
	if plug_addresses == "" {
		return fmt.Errorf("expected SHELLY_PLUG_ADDRESS environment variable")
	}

	for _, addr := range strings.Split(plug_addresses, ",") {
		p := Plug{addr: addr}
		s, err := p.getSwitchStatus()
		if err != nil {
			return fmt.Errorf("error getting switch status for shelly %s: %w", p.addr, err)
		}
		labels := prometheus.Labels{"id": strconv.Itoa(s.Id)}
		power.With(labels).Set(s.Apower)
		voltage.With(labels).Set(s.Voltage)
		current.With(labels).Set(s.Current)
		energy.With(labels).Set(s.Aenergy.total)
	}

	return nil
}

type Plug struct {
	addr string
}

func (p Plug) getSwitchStatus() (SwitchStatus, error) {
	u := url.URL{
		Scheme:   "http",
		Host:     p.addr,
		Path:     "rpc/Switch.GetStatus",
		RawQuery: "id=0",
	}

	res, err := http.Get(u.String())
	if err != nil {
		return SwitchStatus{}, fmt.Errorf("error fetching %s: %w", u.String(), err)
	}

	var status SwitchStatus
	if err = json.NewDecoder(res.Body).Decode(&status); err != nil {
		return SwitchStatus{}, fmt.Errorf("unable to parse json: %w", err)
	}

	return status, nil
}

type SwitchStatus struct {
	// Id of the Switch component instance
	Id int

	// Source of the last command, for example: init, WS_in, http, ...
	Source string

	// true if the output channel is currently on, false otherwise
	Output bool

	//Last measured instantaneous active power (in Watts) delivered to the
	//attached load (shown if applicable)
	Apower float64

	// Last measured voltage in Volts (shown if applicable)
	Voltage float64

	// Last measured current in Amperes (shown if applicable)
	Current float64

	// Information about the active energy counter (shown if applicable)
	Aenergy struct {
		total float64 // Total energy consumed in Watt-hours
	}

	//  Information about the temperature
	Temperature struct {
		// Temperature in Celsius (null if the temperature is out of the
		// measurement range)
		TC float64
	}
}
