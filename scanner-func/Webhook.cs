namespace Webhook
{
    public class Payload
    {
        public string id;
        public string action;
        public Target target;
        public Request request;
    }

    public class Target
    {
        public string mediaType;
        public string digest;
        public int size;
        public string repository;
    }

    public class Request
    {
        public string host;
    }
}
