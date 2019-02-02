package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/manifest/schema1"
	"github.com/docker/distribution/manifest/schema2"
	"github.com/docker/distribution/reference"
	"github.com/pkg/errors"
)

var (
	// ErrResourceNotFound indicates that an image is not available in the registry.
	ErrResourceNotFound = fmt.Errorf("registry returned status 404 NOT FOUND")
	// ErrSchemaV1NotSupported indicates that this library does not support registry v1
	ErrSchemaV1NotSupported = fmt.Errorf("registry schema v1 is not supported by this library")
	// ErrSchemaUnknown indicates that the registry returned an unknown manifest schema
	ErrSchemaUnknown = fmt.Errorf("registry returned an unknown manifest schema")
)

// DefaultClient returns a http.Client with a reasonable timeout.
func DefaultClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
	}
}

// ParseImageName parses the name of an image and reurns its parts.
func ParseImageName(imageName string) (domain, path, tag, digest string, err error) {
	named, err := reference.ParseNormalizedNamed(imageName)
	if err != nil {
		return "", "", "", "", err
	}

	domain = reference.Domain(named)
	path = reference.Path(named)

	if tagged, ok := named.(reference.Tagged); ok {
		tag = tagged.Tag()
	}

	if canonical, ok := named.(reference.Canonical); ok {
		digest = canonical.Digest().String()
	}

	return domain, path, tag, digest, nil
}

// Registry exposes the repositories in a registry.
type Registry struct {
	Authenticator Authenticator
	Client        *http.Client
	Domain        string
	Protocol      string
}

// Repository returns one repository in the registry.
// It does not check if the repository actually exists in the registry.
func (r *Registry) Repository(name string) *Repository {
	if r.Authenticator == nil {
		r.Authenticator = NewNullAuthenticator()
	}

	if r.Protocol == "" {
		r.Protocol = "https"
	}

	req := &requester{
		domain:     r.Domain,
		auth:       r.Authenticator,
		client:     r.Client,
		protocol:   r.Protocol,
		repository: name,
	}
	return &Repository{
		Configs:   &ConfigService{r: req},
		Images:    &ImageService{r: req},
		Manifests: &ManifestService{r: req},
		Name:      name,
		Tags:      &TagService{r: req},
	}
}

// RepositoryFromString is a convenience function to create a repository from an image as used in `docker pull`.
func (r *Registry) RepositoryFromString(name string) (*Repository, error) {
	named, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		return nil, err
	}

	if reference.Domain(named) != r.Domain {
		return nil, fmt.Errorf("domain in image '%s' not equal domain in registry object '%s'", reference.Domain(named), r.Domain)
	}

	return r.Repository(reference.Path(named)), nil
}

// Repository exposes the images in a repository in a registry.
type Repository struct {
	Configs   *ConfigService
	Images    *ImageService
	Manifests *ManifestService
	Name      string
	Registry  *Registry
	Tags      *TagService
}

// ConfigService exposes the config of an image in a repository.
type ConfigService struct {
	r *requester
}

// GetV1 returns the manifest schema v1 of an image.
// The manifest schema v1 is exposed as the "config" key in a manifest schema v2.
// Note that the config can only be queried by tag, not by digest.
func (c *ConfigService) GetV1(tag string) (schema1.SignedManifest, error) {
	var m schema1.SignedManifest
	path := fmt.Sprintf("/manifests/%s", tag)
	req, err := c.r.newRequest("GET", path, nil)
	if err != nil {
		return m, err
	}

	req.Header.Add("Accept", schema2.MediaTypeImageConfig)
	_, err = c.r.getJSON(req, &m)
	if err != nil {
		return m, errors.Wrapf(err, "reading config v1 '%s'", tag)
	}

	return m, nil
}

// ManifestService exposes the manifest of an image in a repository.
type ManifestService struct {
	r *requester
}

// Get returns the manifest schema v2 of an image.
func (p *ManifestService) Get(digest string) (schema2.Manifest, error) {
	var m schema2.Manifest
	path := fmt.Sprintf("/manifests/%s", digest)
	req, err := p.r.newRequest("GET", path, nil)
	if err != nil {
		return m, err
	}

	req.Header.Add("Accept", schema2.MediaTypeManifest)
	_, err = p.r.getJSON(req, &m)
	if err != nil {
		return m, errors.Wrapf(err, "reading manifest '%s'", digest)
	}

	return m, nil
}

// Platform is the platform on which an image can run.
type Platform struct {
	Architecture string
	Digest       string
	Features     []string
	MediaType    string
	OS           string
	OSFeatures   []string
	OSVersion    string
	Size         int
	Variant      string
}

// Image is an identifiable resource in a repository.
// An Image can be identified by a tag or a digest.
// A tag (e.g. "lastest") can move between images.
// A digest is unique within the repository and does not change unless teh image is deleted.
type Image struct {
	Digest     string
	Domain     string
	Platforms  []Platform
	Repository string
	Tag        string
}

// ImageService exposes images.
type ImageService struct {
	r    *requester
	repo *Repository
}

// GetByDigest queries the repository for an image identified by its digest.
// The `Tag` field of an image returned by this method always is an empty string.
func (i *ImageService) GetByDigest(digest string) (Image, error) {
	var img Image
	img, err := i.get(digest)
	if err != nil {
		return img, err
	}

	img.Domain = i.r.domain
	img.Repository = i.r.repository
	return img, nil
}

// GetByTag queries the repository for an image identified by its tag.
func (i *ImageService) GetByTag(tag string) (Image, error) {
	var img Image
	img, err := i.get(tag)
	if err != nil {
		return img, err
	}

	img.Domain = i.r.domain
	img.Repository = i.r.repository
	img.Tag = tag
	return img, nil
}

func (i *ImageService) get(ref string) (Image, error) {
	var img Image
	path := fmt.Sprintf("/manifests/%s", ref)
	req, err := i.r.newRequest("GET", path, nil)
	if err != nil {
		return img, err
	}

	req.Header.Add("Accept", fmt.Sprintf("%s,%s;q=0.9", schema2.MediaTypeManifest, manifestlist.MediaTypeManifestList))
	data, headers, err := i.r.getByte(req)
	m, _, err := distribution.UnmarshalManifest(headers.Get("Content-Type"), data)
	if err != nil {
		return img, errors.Wrapf(err, "unmarshalling manifest '%s'", ref)
	}

	img.Digest = headers.Get("Docker-Content-Digest")
	switch manifest := m.(type) {
	case *schema2.DeserializedManifest:
		p := Platform{
			Architecture: "amd64",
			Digest:       headers.Get("Docker-Content-Digest"),
			Features:     []string{},
			MediaType:    headers.Get("Content-Type"),
			OS:           "linux",
			OSFeatures:   []string{},
			OSVersion:    "",
			Size:         0,
			Variant:      "",
		}
		img.Platforms = append(img.Platforms, p)
	case *manifestlist.DeserializedManifestList:
		for _, platformManifest := range manifest.Manifests {
			img.Platforms = append(img.Platforms, Platform{
				Architecture: platformManifest.Platform.Architecture,
				Digest:       platformManifest.Digest.String(),
				Features:     platformManifest.Platform.Features,
				MediaType:    platformManifest.MediaType,
				OS:           platformManifest.Platform.OS,
				OSFeatures:   platformManifest.Platform.OSFeatures,
				OSVersion:    platformManifest.Platform.OSVersion,
				Size:         int(platformManifest.Size),
				Variant:      platformManifest.Platform.Variant,
			})
		}
	case *schema1.SignedManifest:
		return img, ErrSchemaV1NotSupported
	default:
		return img, ErrSchemaUnknown
	}

	return img, nil
}

type tagGetAllResponse struct {
	Tags []string
}

// TagService exposes tags in a repository.
type TagService struct {
	r *requester
}

// GetAll returns all tags in the repository.
func (r *TagService) GetAll() ([]string, error) {
	req, err := r.r.newRequest("GET", "/tags/list", nil)
	if err != nil {
		return nil, err
	}

	tagsResponse := tagGetAllResponse{}
	_, err = r.r.getJSON(req, &tagsResponse)
	if err != nil {
		return nil, errors.Wrap(err, "reading tags")
	}

	return tagsResponse.Tags, nil
}

type requester struct {
	auth       Authenticator
	client     *http.Client
	domain     string
	protocol   string
	repository string
}

func (r *requester) getByte(req *http.Request) ([]byte, http.Header, error) {
	resp, err := r.sendRequest(req)
	if err != nil {
		return nil, nil, err
	}

	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, ErrResourceNotFound
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, errors.Wrap(err, "reading response")
	}

	return data, resp.Header, nil
}

func (r *requester) getJSON(req *http.Request, out interface{}) (http.Header, error) {
	data, headers, err := r.getByte(req)

	err = json.Unmarshal(data, out)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshalling JSON")
	}

	return headers, nil
}

func (r *requester) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	domain := r.domain
	if domain == "docker.io" {
		domain = "index.docker.io"
	}

	url := fmt.Sprintf("%s://%s/v2/%s%s", r.protocol, domain, r.repository, path)
	return http.NewRequest(method, url, body)
}

func (r *requester) sendRequest(req *http.Request) (*http.Response, error) {
	err := r.auth.HandleRequest(req)
	if err != nil {
		return nil, errors.Wrap(err, "handling authenticator request")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "querying '%s'", req.URL.String())
	}

	resp, resend, err := r.auth.HandleResponse(resp)
	if err != nil {
		return nil, errors.Wrap(err, "handling authenticator response")
	}

	if resend {
		return r.sendRequest(req)
	}

	return resp, nil
}
