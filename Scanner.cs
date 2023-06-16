using System.IO;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Mvc;
using Microsoft.Azure.WebJobs;
using Microsoft.Azure.WebJobs.Extensions.Http;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Newtonsoft.Json;
using Webhook;
using Azure.Containers.ContainerRegistry;
using System;
using Azure.Identity;
using System.Text.RegularExpressions;
using Azure.Core;
using Azure.Core.Pipeline;
using System.Threading;

namespace ScannerFunc
{
    public static class Scanner
    {
        [FunctionName("Webhook")]
        public static async Task<IActionResult> Run(
            [HttpTrigger(AuthorizationLevel.Function, "post", Route = null)] HttpRequest req,
            ILogger log)
        {
            string requestBody = await new StreamReader(req.Body).ReadToEndAsync();
            Payload payload = JsonConvert.DeserializeObject<Payload>(requestBody);

            if (string.IsNullOrEmpty(payload?.action))
            {
                return new BadRequestObjectResult("Missing action in payload");
            }

            if (payload.action != "quarantine")
            {
                return new OkObjectResult("Not quarantine event, skipped.");
            }

            if (string.IsNullOrEmpty(payload.target?.digest) || string.IsNullOrEmpty(payload.target?.repository) || string.IsNullOrEmpty(payload.request?.host))
            {
                return new BadRequestObjectResult("Missing digest/repository/host in payload.");
            }

            // Fire-and-forget in a webhook trigger is anti-pattern. Should not be used in production code.
            var _ = Task.Run(async () => await ScanAsync(log, payload.request.host, payload.target.repository, payload.target.digest));

            string responseMessage = $"Enqueued scan task for {payload.target.repository}@{payload.target.digest}";
            return new OkObjectResult(responseMessage);
        }

        private static async Task ScanAsync(ILogger log, string registry, string repository, string digest)
        {
            try
            {
                Uri endpoint = new Uri($"https://{registry}");
                ContainerRegistryContentClient client = new(endpoint, repository, new DefaultAzureCredential());
                GetManifestResult result = await client.GetManifestAsync(digest);
                OciImageManifest manifest = result.Manifest.ToObjectFromJson<OciImageManifest>();

                foreach (var layer in manifest.Layers)
                {
                    // Demo only, do not download all to memory in production code.
                    DownloadRegistryBlobResult blob = await client.DownloadBlobContentAsync(layer.Digest);
                    string content = blob.Content.ToString();
                    if (!ValidateBicep(content))
                    {
                        log.LogInformation($"Stop scanning image, found non-Bicep file: {layer}");
                        return;
                    }
                }

                ClearQuarantineFlag(log, client.Pipeline, registry, repository, digest);
            } catch (Exception ex)
            {
                log.LogError(ex.ToString());
            }
        }

        private static async void ClearQuarantineFlag(ILogger log, HttpPipeline pipeline, string registry, string repository, string digest)
        {
            var message = pipeline.CreateMessage();
            message.Request.Method = RequestMethod.Patch;
            message.Request.Uri.Reset(new Uri($"https://{registry}/acr/v1/{repository}/_manifests/{digest}"));
            message.Request.Headers.Add("Content-Type", "application/json");
            message.Request.Content = "{ \"quarantineState\": \"Passed\", \"quarantineDetails\": \"{\\\"state\\\":\\\"scan passed\\\",\\\"link\\\":\\\"http://example.com\\\"}\" }";
            await pipeline.SendAsync(message, CancellationToken.None);

            var response = message.Response;
            if (response.Status != StatusCodes.Status200OK)
            {
                log.LogError($"Failed to clear quarantine flag for {repository}@{digest}: {response.Status} {response.Content}");
            }

            log.LogInformation($"Quarantine flag cleared for {repository}@{digest}");
        }

        private static readonly Regex reBicepStrong = new Regex(@"^\s*(metadata|targetScope|resource|module|output)\s");
        private static readonly Regex reBicepWeak = new Regex(@"^\s*(param|var)\s");

        private static bool ValidateBicep(string s)
        {
            Match match = reBicepStrong.Match(s);
            if (match.Success)
            {
                return true;
            }

            MatchCollection matches = reBicepWeak.Matches(s[..Math.Min(1024, s.Length)]);
            return matches.Count > 1;
        }
    }
}
