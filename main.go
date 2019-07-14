package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-github/v26/github"
	"github.com/imagespy/registry-client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	log "github.com/sirupsen/logrus"
)

const (
	promNamespace = "imagespy_hub_discoverer"
)

var (
	imagespyAPIAddress     = flag.String("imagespy.api", "https://localhost:8080", "Address of an imagespy API server")
	logLevel               = flag.String("log.level", "error", "Log Level")
	promPushgatewayAddress = flag.String("prometheus.pushgateway", "", "Address of a Prometheus Pushgateway to send metrics to (optional)")

	promCompletionTime = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Name:      "last_completion_timestamp_seconds",
		Help:      "The timestamp of the last completion of discover run, successful or not.",
	})

	promDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: promNamespace,
		Name:      "duration_seconds",
		Help:      "The duration of the last update run in seconds.",
	})
)

func mustInitLogging() {
	lvl, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal(err)
	}

	log.SetLevel(lvl)
}

func main() {
	flag.Parse()
	mustInitLogging()
	start := time.Now()
	httpClient := &http.Client{}
	client := github.NewClient(httpClient)
	_, dirContent, _, err := client.Repositories.GetContents(context.Background(), "docker-library", "official-images", "/library", &github.RepositoryContentGetOptions{Ref: "master"})
	if err != nil {
		log.Fatal(err)
	}

	reg := registry.Registry{
		Authenticator: registry.NewTokenAuthenticator(),
		Client:        registry.DefaultClient(),
		Domain:        "docker.io",
		Protocol:      "https",
	}
	for _, c := range dirContent {
		if *c.Type != "file" {
			continue
		}

		log.Debugf("found hub '%s'", *c.Name)
		repo := reg.Repository("library/" + *c.Name)
		tags, err := repo.Tags.GetAll()
		if err != nil {
			log.Errorf("get all tags of repository '%s': %v", repo.Name, err)
			continue
		}

		for _, tag := range tags {
			imageName := *c.Name + ":" + tag
			log.Debugf("discovering image '%s'", imageName)
			url := fmt.Sprintf("%s/v2/images/%s", *imagespyAPIAddress, imageName)
			resp, err := httpClient.Post(url, "text/plain", nil)
			if err != nil {
				log.Fatalf("send discover request to imagespy api: %v", err)
			}

			switch resp.StatusCode {
			case http.StatusConflict:
				log.Debugf("image '%s' already discovered", imageName)
			case http.StatusCreated:
				log.Debugf("new image '%s' discovered", imageName)
			default:
				log.Errorf("discovering image '%s' failed: %s", imageName, resp.Status)
			}
		}
	}

	promCompletionTime.SetToCurrentTime()
	promDuration.Set(time.Since(start).Seconds())
	if *promPushgatewayAddress != "" {
		registry := prometheus.NewRegistry()
		registry.MustRegister(promCompletionTime, promDuration)
		err := push.New(*promPushgatewayAddress, promNamespace).Gatherer(registry).Add()
		if err != nil {
			log.Fatalf("pushing to pushgateway: %s", err)
		}
	}
}
