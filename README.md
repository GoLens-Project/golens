# Project Specification: GoLens

## 1. Overview

**GoLens** is a lightweight, declarative observability middleware for Go applications. It provides a "Swagger-like" developer experience by enabling automatic metric collection through middleware, while decoupling the **Collection (Middleware)**, **Registry (Source of Truth)**, and **Exposition (UI/OTEL)** layers.

GoLens is designed to be "zero-config" for small services while remaining production-ready for high-throughput environments via standard OpenTelemetry (OTLP) integration.

---

## 2. Architectural Design: The 3-Layered Approach

### Layer 1: The Collector (Middleware)

The Collector acts as the entry point. It intercepts incoming HTTP/gRPC requests, extracts performance data (RED metrics: Rate, Errors, Duration), and forwards them to the Registry.

* **Auto-Instrumentation:** Automatically records standard request metrics without developer intervention.
* **Custom Hooks:** Provides a simple API for developers to record domain-specific business metrics (e.g., `inventory_count`, `user_signup_event`).

### Layer 2: The Registry (Source of Truth)

The Registry is a thread-safe (or performance-optimized) container that maintains the current state of the application's telemetry.

* **Concurrency Modes:**
* `SafeMode`: Uses `sync.RWMutex` to handle high-concurrency environments.
* `FastMode`: Uses `atomic` primitives and unsafe pointers for high-throughput, low-latency needs.


* **Storage Strategy:**
* **Hot Ingestion:** RAM-based with atomic operations to prevent blocking the request lifecycle.
* **Optional Persistence:** Background aggregation of raw metrics into summarized SQLite buckets for historical analysis.



### Layer 3: The Exposition Layer (Presentation & Export)

This layer handles how the world sees your telemetry. It is designed to be "Swagger-like," where the documentation *is* the interface.

* **HTMX UI:** An embedded, server-side rendered dashboard (mounted at `/metrics`) that provides an interactive, searchable view of metrics, using non-aggressive HTMX polling.
* **OTLP Exporter:** Provides machine-readable interfaces (gRPC/HTTP) that comply with the OpenTelemetry standard, allowing for seamless integration with backends like Prometheus, Grafana Mimir, or VictoriaMetrics.

---

## 3. Core Features

* **"Swagger-Inspired" Discovery:** View all registered metrics and their definitions via a clean, auto-generated browser interface.
* **Flexible Telemetry (The Freedom Principle):** Users choose their persistence model (RAM only vs. local SQLite) and their export protocol (OTLP gRPC vs. OTLP HTTP).
* **Non-Aggressive Polling:** UI refreshes are triggered by HTMX on a customizable timer, defaulting to a low-impact 5-second interval.
* **Performance Tiers:** Explicit configuration to toggle between thread-safe and non-concurrent operation modes.

---

## 4. Implementation Guidelines

### Developer Workflow

1. **Initialize:** Define the `GoLens` registry with your desired concurrency strategy and optional persistence settings (e.g., SQLite path).
2. **Mount:** Add the `GoLensMiddleware` to your router.
3. **Consume:** * Access `/metrics` for the interactive HTMX dashboard.
* Configure your OTEL backend (Prometheus/Grafana) to scrape the OTLP-compliant endpoints.



### Persistence Strategy

* **In-Memory:** Primary storage for high-frequency ingestion.
* **SQLite (Optional):** Used exclusively for **summarized data (roll-ups)** to provide historical insights (e.g., hourly averages) without impacting runtime performance.
* **OTLP:** The primary mechanism for **Production-Grade long-term storage**, shifting the burden of retention and query-heavy processing to purpose-built TSDBs.

---

## 5. Summary of Choices

| Component | Responsibility | Recommended Implementation |
| --- | --- | --- |
| **Ingestion** | Collect metrics | `sync/atomic` (Fast) or `RWMutex` (Safe) |
| **Persistence** | Durability | OTLP (Remote) / SQLite (Local Summary) |
| **UI** | Visualization | HTMX + `html/template` |
| **Protocol** | Standard | OpenTelemetry (OTLP) |
