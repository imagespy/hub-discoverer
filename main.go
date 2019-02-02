package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"github.com/google/go-github/github"
	"github.com/imagespy/registry-client"
	log "github.com/sirupsen/logrus"
)

var (
	imagespyAPIAddress = flag.String("imagespy.api", "https://localhost:8080", "Address of an imagespy API server")
	logLevel           = flag.String("log.level", "error", "Log Level")
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
			log.Fatal(err)
		}

		for _, tag := range tags {
			imageName := *c.Name + ":" + tag
			log.Debugf("discovering image '%s'", imageName)
			url := fmt.Sprintf("%s/v2/images/%s", *imagespyAPIAddress, imageName)
			resp, err := httpClient.Post(url, "text/plain", nil)
			if err != nil {
				log.Fatal(err)
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
}
