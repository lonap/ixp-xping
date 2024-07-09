LONAP Deployments configuration changes from the base install
===

## /etc/default/prometheus

```
ARGS="--storage.tsdb.retention.time=90d"
```

> This is to ensure that data is kept around for at least 90 days. Every 15 days is around 3GB, Resulting in a target disk usage of 18GB


## /etc/grafana/grafana.ini

```
[users]
# disable user signup / registration
allow_sign_up = false


#################################### Anonymous Auth ######################
[auth.anonymous]
# enable anonymous access
enabled = true
org_name=LONAP

```

> This is to allow users to see the data without authentication, be aware that this will also expose all other dashboards **and** the data inside the prometheus, if you are going to collect any other data on this host that you do not wish to be visible to outsiders, you must collect it on a different prometheus setup.


### Prometheus

Adding recording rules can vastly speed up the grafana interface, and likely makes a good CPU improvemnt for alerting as well.

Added `/etc/prometheus/prometheus.yml`

```
rule_files:
  - "/etc/prometheus/rules.yml"
```

Added: `/etc/prometheus/rules.yml`

```
groups:
  - name: recording
    rules:
    - record: avgSwitchPairLatency
      expr: avg by(local, peer) (xping_peer_latency_per_flow)
    - record: maxSwitchPairLoss
      expr: max by(local, peer) (xping_peer_loss_per_flow)
```