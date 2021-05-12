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
  "balancingAlgorithm": "weighted_round_robin", // supports 3 algorithms currently - round_robin, weighted_round_robin, source_ip_hash
  "weights":{
    "http://localhost:6060":1,
    "http://localhost:7070":10,
    "http://localhost:8080":5,
  },
  "port": 1234 // port which the load balancer runs on
}
```
The "weights" key is only needed when using weighted_round_robin. If weighted_round_robin is used and the weights are either not specified or not sufficiently specified for each of the backend urls, a default weight of 1 (weight of 1 for each backend, making it regular round robin) is used.  
