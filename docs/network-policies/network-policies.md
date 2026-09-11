# OSC Network Policies — Component by Component

## kata-monitor (3 NPs)

```mermaid
graph TB
    PROM["Prometheus (host-network)"]

    PROM -- "B2: kata-monitor-allow-metrics-ingress<br/>TCP 8443 · annotation: host-network" --> MON

    MON["<b>kata-monitor pod</b><br/>label: name=openshift-sandboxed-containers-monitor<br/>DaemonSet · port 8443/TCP"]

    MON -- "B3: kata-monitor-allow-dns-egress<br/>TCP+UDP 5353" --> DNS["CoreDNS (openshift-dns)"]

    DENY["B1: kata-monitor-deny-all<br/>❌ all other ingress + egress blocked"]

    DENY -.- MON

    style DENY fill:#ffcccc,stroke:#cc0000
```

## peer-pods-webhook (5 NPs)

```mermaid
graph TB
    APISERVER_IN["kube-apiserver (host-network)"]
    PROM["Prometheus (host-network)"]

    APISERVER_IN -- "C2: peer-pods-webhook-allow-webhook-ingress<br/>TCP 9443 · annotation: host-network" --> WH
    PROM -- "C3: peer-pods-webhook-allow-metrics-ingress<br/>TCP 8443 · annotation: host-network" --> WH

    WH["<b>peer-pods-webhook pod</b><br/>label: app=peer-pods-webhook<br/>Deployment (2 replicas) · ports 9443, 8443/TCP"]

    WH -- "C4: peer-pods-webhook-allow-dns-egress<br/>TCP+UDP 5353" --> DNS["CoreDNS (openshift-dns)"]
    WH -- "C5: peer-pods-webhook-allow-apiserver-egress<br/>all egress" --> APISERVER_OUT["kube-apiserver"]

    DENY["C1: peer-pods-webhook-deny-all<br/>❌ all other ingress + egress blocked"]

    DENY -.- WH

    style DENY fill:#ffcccc,stroke:#cc0000
```

## kata-install (2 NPs)

```mermaid
graph TB
    DENY["D1: kata-install-deny-all<br/>❌ all ingress blocked"]

    DENY -.- INST

    INST["<b>kata-install pod</b><br/>label: name=osc-rpm-install<br/>DaemonSet (transient) · no ports"]

    INST -- "D2: kata-install-allow-egress<br/>all egress" --> EXTERNAL["All external (registries, API server, DNS)"]

    style DENY fill:#ffcccc,stroke:#cc0000
```

## kata-uninstall (2 NPs)

```mermaid
graph TB
    DENY["D3: kata-uninstall-deny-all<br/>❌ all ingress blocked"]

    DENY -.- UNINST

    UNINST["<b>kata-uninstall pod</b><br/>label: name=osc-rpm-uninstall<br/>DaemonSet (transient) · no ports"]

    UNINST -- "D4: kata-uninstall-allow-egress<br/>all egress" --> EXTERNAL["All external (API server, DNS)"]

    style DENY fill:#ffcccc,stroke:#cc0000
```

## podvm-image-creation (2 NPs)

```mermaid
graph TB
    DENY["E1: podvm-image-creation-deny-all<br/>❌ all ingress blocked"]

    DENY -.- IMGC

    IMGC["<b>podvm-create pod</b><br/>label: job-name=osc-podvm-image-creation<br/>Job (transient) · no ports"]

    IMGC -- "E2: podvm-image-creation-allow-egress<br/>all egress" --> EXTERNAL["All external (cloud APIs, registries, DNS)"]

    style DENY fill:#ffcccc,stroke:#cc0000
```

## podvm-image-deletion (2 NPs)

```mermaid
graph TB
    DENY["E3: podvm-image-deletion-deny-all<br/>❌ all ingress blocked"]

    DENY -.- IMGD

    IMGD["<b>podvm-delete pod</b><br/>label: job-name=osc-podvm-image-deletion<br/>Job (transient) · no ports"]

    IMGD -- "E4: podvm-image-deletion-allow-egress<br/>all egress" --> EXTERNAL["All external (cloud APIs, DNS)"]

    style DENY fill:#ffcccc,stroke:#cc0000
```

## cloud-api-adaptor — NO NPs

```mermaid
graph TB
    CAA["<b>caa pod</b><br/>label: name=osc-caa-ds<br/>DaemonSet · hostNetwork=true"]

    CAA -- "hostNetwork=true<br/>NetworkPolicy does not apply" --> CLOUD["Cloud APIs / API server"]

    style CAA fill:#ffffcc,stroke:#cccc00,stroke-width:2px
```

## controller-manager — Phase 2 OLM bundle (pending)

```mermaid
graph TB
    APISERVER_IN["kube-apiserver (host-network)"]
    PROM["Prometheus (host-network)"]

    APISERVER_IN -- "A2: controller-manager-allow-webhook-ingress<br/>TCP 9443 · annotation: host-network" --> OP
    PROM -- "A3: controller-manager-allow-metrics-ingress<br/>TCP 8443 · annotation: host-network" --> OP

    OP["<b>controller-manager pod</b><br/>label: control-plane=controller-manager<br/>Deployment · ports 9443, 8443/TCP"]

    OP -- "A4: controller-manager-allow-dns-egress<br/>TCP+UDP 5353" --> DNS["CoreDNS (openshift-dns)"]
    OP -- "A5: controller-manager-allow-apiserver-egress<br/>all egress" --> APISERVER_OUT["kube-apiserver"]

    DENY["A1: controller-manager-deny-all<br/>❌ all other ingress + egress blocked"]

    DENY -.- OP

    style DENY fill:#ffcccc,stroke:#cc0000
    style OP fill:#e0e0e0,stroke:#999999,stroke-width:2px,stroke-dasharray: 5 5
```
