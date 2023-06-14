# What's this

This a sample project that demonstrates how to use ACR webhook and quarantine feature to implement custom image scanning logic. The target scenario is a container registry that filters image pushes by content. Only images that pass scan are pullable.

A newly pushed image will go through the following flow:
1. Image is quarantined. At this stage, users without quarantine read permission cannot access the image.
1. Scanner is notified of the image push via webhook.
1. Scanner scans image (manifest and blobs) content. 
1. If image passes check, scanner will clear quarantine flag on the image, which makes it pullable for all users.
1. Otherwise, the image remains quarantined.

A webhook is implemented in Go to scan images and check for Bicep files. The webhook can be deployed to Azure App Service.


# Try it out

## Prerequisites

- An Azure subscription.
- [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/)
- [Oras CLI](https://oras.land/docs/cli/installation)

## Create Azure Container Registry

    az group create --name myResourceGroup --location eastus
    az acr create --resource-group myResourceGroup --name scannertestcr --sku Basic

Login to verify the registry works:

    az acr login -n scannertestcr

## Enable registry quarantine state

Registry quarantine state can be checked by:

    az acr show -n scannertestcr | jq -r ".policies.quarantinePolicy"

Enable quarantine state with [this API](https://docs.microsoft.com/en-us/rest/api/containerregistry/registries/update#policies). We can use the "Try it" tool on the doc page to auth & send request. The request body is:

```json
{
  "properties": {
    "policies": {
      "quarantinePolicy": {
        "status": "enabled"
      }
    }
  }
}
```

Verify quarantine works. Note that in the execution below, newly pushed image is quarantined.

```sh
$ oras push scannertestcr.azurecr.io/scanner-test:v1 hi.txt
Exists    b5bb9d8014a0 hi.txt
Pushed [registry] scannertestcr.azurecr.io/scanner-test:v1
Digest: sha256:d4129e7a956ed858fdfbe75a004b95c1b626048028f23915b79e10508c1359d7

# Fetch with an identity that doesn't have quaratine role fails:
$ oras manifest fetch scannertestcr.azurecr.io/scanner-test@sha256:d4129e7a956ed858fdfbe75a004b95c1b626048028f23915b79e10508c1359d7
Error: failed to fetch the content of "scannertestcr.azurecr.io/scanner-test@sha256:d4129e7a956ed858fdfbe75a004b95c1b626048028f23915b79e10508c1359d7": GET "https://scannertestcr.azurecr.io/v1/scanner-test/manifests/sha256:d4129e7a956ed858fdfbe75a004b95c1b626048028f23915b79e10508c1359d7": response status code 403: image quarantined: The image is quarantined.: map[]
```

## Create App Service

Create an App Service hosting Go program in this project ([reference](https://learn.microsoft.com/en-us/azure/app-service/quickstart-golang)):

    git clone git@github.com:m5i-work/acr-scanner.git
    cd acr-scanner
    az login
    az webapp up --runtime GO:1.19 --os linux --sku B1 --name scanner-webhook --location "East US"

This creates an App Service instance of B1 (Basic) service plan, named `scanner-webhook` in `East US`. If an instance with the same name already exists in your subscription, it's reused.

For ease of debugging, follow [the instruction](https://aka.ms/linux-diagnostics) to turn on app logs. After that, logs can be watched from "Log stream" blade.

## Integrate App Service with ACR

Next, we need to grant permissions to the App Service to pull images and toggle quarantine state.

1. Add a system-assigned identity for app service([doc](https://learn.microsoft.com/en-us/azure/app-service/overview-managed-identity?tabs=portal%2Chttp#add-a-system-assigned-identity)). Take a note of Object (principal) ID.
1. In ACR registry, [add role assignment](https://learn.microsoft.com/en-us/azure/container-registry/container-registry-roles?tabs=azure-cli#assign-roles) for the identity. Required roles are `AcrQuarantineWriter` and `AcrPush`.
1. Add an ACR webhook that calls App Service. The action is `quarantine`. Host of the uri is the same as App Service.
   ```
   az acr webhook create -n scanner -r scannertestcr --uri https://scanner-webhook.azurewebsites.net/hook --actions quarantine
   ```

## Verify webhook works

First, let's push an OCI image manifest with a normal plain text file as layer:

    echo "foo" > hi.txt
    oras push scannertestcr.azurecr.io/scanner-test:v1 hi.txt

Since this file is not a Bicep file, it gets quarantined:

    $ oras manifest fetch scannertestcr.azurecr.io/scanner-test:hi1515
    Error: failed to fetch the content of "scannertestcr.azurecr.io/scanner-test:v1": scannertestcr.azurecr.io/scanner-test:v1: not found

We can also check scanner logs on App Service to see what's happening:

    Scanning image: scanner-test:sha256:65fefeccfc30b9fee4a8e484497ce0dd8b4c31c37d9234f1bfcf3ac2bc59066a
    Stop scanning image, found non-Bicep file: {application/vnd.oci.image.layer.v1.tar sha256:d78931fcf2660108eec0d6674ecb4e02401b5256a6b5ee82527766ef6d198c67 8 [] map[org.opencontainers.image.title:hi.txt] [] <nil> }

Next, let's repeat the above with a Bicep file instead. There's a sample Bicep file in `samples`.

    oras push scannertestcr.azurecr.io/scanner-test:v2 samples/foo.bicep

We can find in scanner logs that this image is moved out of quarantine state:

    Scanning image: scanner-test:sha256:cb8293487d5f197abf0bfc3e8cb88c47430681b711f1ae0444c48dea66470a8f
    Quarantine flag cleared for: scanner-test:sha256:cb8293487d5f197abf0bfc3e8cb88c47430681b711f1ae0444c48dea66470a8f

Therefore, it is pullable:

    $ oras manifest fetch scannertestcr.azurecr.io/scanner-test:v2
    {"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.unknown.config.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:bff6730639c5cce9a8d5550a709f444d90d746feedce59aec7b5d1072ff8ab2b","size":697,"annotations":{"org.opencontainers.image.title":"foo.bicep"}}],"annotations":{"org.opencontainers.image.created":"2023-06-14T07:22:11Z"}}


## Troubleshooting

Check the following if webhook doesn't work:
1. Check "Webhook" blade of ACR web portal, verify that webhook is called.
1. Check if there's any error in webhook response. Upon success, the response should be like:
   ```
   Enqueued scan task for scanner-test@sha256:c9680c500f2bd67a5e9b48083842f02c9380d61207d56e7be93313b889d41589
   ```
1. Check App Service deployment log to find errors.
1. Check App Service application log to find errors.


## Clean up resources

When no longer needed, you can use the `az group delete` command to remove the resource group, and all related resources:

    az group delete --resource-group <resource-group-name>
