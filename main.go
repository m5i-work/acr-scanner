package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"m5i.scanner/webhook"
	"oras.land/oras-go/v2/registry/remote/auth"
)

const (
	REGISTRY = "scannertestcr.azurecr.io"
)

func main() {
	ctx := context.Background()
	client, err := NewRepoClient(ctx, REGISTRY)
	if err != nil {
		panic(err)
	}

	ch := make(chan webhook.Task)
	scanner := &webhook.Scanner{
		Registry: REGISTRY,
		Client:   client,
		Tasks:    ch,
	}
	go func() {
		if err := scanner.Run(); err != nil {
			panic(err)
		}
	}()

	webhook := &webhook.WebhookHandler{
		Tasks: ch,
	}
	http.HandleFunc("/hook", webhook.Handle)
	http.ListenAndServe(":8080", nil)
}

func NewRepoClient(ctx context.Context, registry string) (*auth.Client, error) {
	aadToken, err := GetAadToken(ctx)
	if err != nil {
		return nil, err
	}

	refreshToken, err := ExchangeAcrRefreshToken(ctx, registry, aadToken)
	if err != nil {
		return nil, err
	}

	// FIXME: refresh token when expired

	return &auth.Client{
		Credential: auth.StaticCredential(registry, auth.Credential{
			RefreshToken: refreshToken,
		}),
	}, nil
}

// Reference: https://learn.microsoft.com/en-us/azure/app-service/overview-managed-identity?tabs=portal%2Cpowershell#connect-to-azure-services-in-app-code
func GetAadToken(ctx context.Context) (string, error) {
	endpoint := os.Getenv("IDENTITY_ENDPOINT")
	header := os.Getenv("IDENTITY_HEADER")
	uri := fmt.Sprintf("%s?resource=https://containerregistry.azure.net&api-version=2019-08-01", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", fmt.Errorf("build msi request: %w", err)
	}
	req.Header.Set("X-IDENTITY-HEADER", header)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("make msi request: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read msi response body: %w", err)
	}

	var msiResp struct {
		AccessToken string `json:"access_token"`
		ExpiresOn   string `json:"expires_on"`
		ClientId    string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &msiResp); err != nil {
		return "", fmt.Errorf("decode msi response json: %w", err)
	}

	return msiResp.AccessToken, nil
}

func ExchangeAcrRefreshToken(ctx context.Context, registry, aadToken string) (string, error) {
	uri := fmt.Sprintf("https://%s/oauth2/exchange", registry)
	postData := url.Values{}
	postData.Set("grant_type", "access_token")
	postData.Set("service", registry)
	postData.Set("access_token", aadToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, strings.NewReader(postData.Encode()))
	if err != nil {
		return "", fmt.Errorf("build exchange request: %w", err)
	}
	req.Header.Set("Host", registry)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("make exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange status !ok: %s", resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read exchange response body: %w", err)
	}

	var exchange struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &exchange); err != nil {
		return "", fmt.Errorf("decode exchange response json: %w", err)
	}

	return exchange.RefreshToken, nil
}
