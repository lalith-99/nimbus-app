# Nimbus Architecture Diagrams

> 📐 **This document has moved.** The architecture reference now lives in
> **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — a deep dive with rendered Mermaid diagrams
> (C4 context/container/component views, request-lifecycle sequence diagrams, the worker flow,
> notification state machine, circuit-breaker FSM, the RAG pipeline, AWS deployment topology, an
> ERD, illustrative workload math, failure-mode analysis, and a design-decision/tradeoff table).

The guide describes durable acceptance, database claim coordination, retries, and dead-letter
recovery, including the crash window between a provider send and its database status update.
Workload calculations and local serialization benchmarks are not measured production capacity.

Continue to **[docs/ARCHITECTURE.md →](docs/ARCHITECTURE.md)**
