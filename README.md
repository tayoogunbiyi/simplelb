# SimpleLB

Simple LB is the simplest Load Balancer ever created.

It supports multiple algorithms to send requests into set of backends and support
retries too.

It also performs active cleaning and passive recovery for unhealthy backends.

Since its simple it assume if / is reachable for any host its available

# How to use
Create a config.json file of the form below
```json
{
  "backendURLs": [
        "http://localhost:6060","http://localhost:7070","http://localhost:8080"
        ],
  "balancingAlgorithm": "round_robin", // supports 2 algorithms currently - round_robin, source_ip_hash
  "port": 1234 // port which the load balancer runs on
}
```
