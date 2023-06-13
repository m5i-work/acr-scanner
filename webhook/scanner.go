package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

type Task struct {
	Repository string
	Digest     digest.Digest
}

type Scanner struct {
	Registry string
	Client   *auth.Client
	Tasks    <-chan Task
}

func (scanner *Scanner) Run() error {
	for t := range scanner.Tasks {
		if err := scanner.scanImage(t.Repository, t.Digest); err != nil {
			return err
		}
	}

	return nil
}

func (scanner *Scanner) scanImage(repository string, digest digest.Digest) error {
	log(fmt.Sprintf("Scanning image: %s:%s", repository, digest))

	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", scanner.Registry, repository))
	if err != nil {
		return fmt.Errorf("new repository: %w", err)
	}
	repo.Client = scanner.Client

	ctx := context.Background()
	descriptor, rc, err := repo.FetchReference(ctx, digest.String())
	if err != nil {
		return fmt.Errorf("fetch manifest descriptor: %w", err)
	}
	defer rc.Close()

	manifestBytes, err := content.ReadAll(rc, descriptor)
	if err != nil {
		return fmt.Errorf("fetch manifest content: %w", err)
	}

	// fetch blobs: TODO: support other manifest types
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("unmarshal manifest content: %w", err)
	}
	for _, blob := range manifest.Layers {
		content, err := content.FetchAll(ctx, repo, blob)
		if err != nil {
			return fmt.Errorf("fetch blob: %w", err)
		}

		if !validateBicep(content) {
			log(fmt.Sprintf("Stop scanning image, found non-Bicep file: %v\n", blob))
			return nil
		}
	}

	if err := scanner.clearQuarantineFlag(ctx, repository, digest); err != nil {
		return err
	}

	log(fmt.Sprintf("Quarantine flag cleared for: %s:%s", repository, digest))
	return nil
}

func (scanner *Scanner) clearQuarantineFlag(ctx context.Context, repository string, digest digest.Digest) error {
	url := fmt.Sprintf("https://%s/acr/v1/%s/_manifests/%s", scanner.Registry, repository, digest.String())
	bodyJson := "{ \"quarantineState\": \"Passed\", \"quarantineDetails\": \"{\\\"state\\\":\\\"scan passed\\\",\\\"link\\\":\\\"https://github.com/m5i-work/acr-scanner\\\"}\" }"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, strings.NewReader(bodyJson))
	if err != nil {
		return fmt.Errorf("build clear quarantine request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := scanner.Client.Do(req)
	if err != nil {
		return fmt.Errorf("make clear quarantine request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return errors.New("error reading clear quarantine request body")
		}

		return fmt.Errorf("clear quarantine request: %s, %s", resp.Status, string(body))
	}

	return nil
}

func log(s string) {
	fmt.Println(s)
}

var (
	reBicepStrong = regexp.MustCompile(`^\s*(metadata|targetScope|resource|module|output)\s`)
	reBicepWeak   = regexp.MustCompile(`^\s*(param|var)\s`)
)

// FIXME: quick & dirty hack
// Ref: https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/file#bicep-format
func validateBicep(s []byte) bool {
	if reBicepStrong.FindIndex(s) != nil {
		return true
	}

	m := reBicepWeak.FindAllIndex(s, 2)
	return len(m) > 1
}
