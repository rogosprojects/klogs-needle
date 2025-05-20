# 🔍 klogs-needle

[![Go Version](https://img.shields.io/badge/Go-1.24-blue.svg)](https://golang.org/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-Compatible-326CE5.svg)](https://kubernetes.io/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A simple Go application that monitors Kubernetes pod logs for specific string patterns. This tool is designed to be used in CI/CD pipelines to verify successful deployments by confirming that expected log messages appear after deploying microservices to Kubernetes.



## 📋 Table of Contents

- [Overview](#-overview)
- [Features](#-features)
- [Installation](#-installation)
- [Usage](#-usage)
- [Examples](#-examples)
- [Configuration](#-configuration)
- [Exit Codes](#-exit-codes)
- [Running in Kubernetes](#-running-in-kubernetes)
- [Contributing](#-contributing)

## 🔭 Overview

klogs-needle solves a common problem in Kubernetes deployments: verifying that applications have started correctly by monitoring their logs for specific patterns. Instead of manually checking logs or writing custom scripts for each deployment, klogs-needle provides a standardized way to wait for expected log messages, making it ideal for integration into CI/CD pipelines (for example, in a GitLab pipeline).

## ✨ Features

- 🔄 Connects to Kubernetes using in-cluster configuration or local kubeconfig
- 🌐 Works both inside and outside Kubernetes clusters
- 📊 Watches logs from a specified pod/container or all pods in a deployment or statefulset
- 🔍 Searches for a specific string pattern in the logs
- ✅ Exits with success (0) when the pattern is found
- ❌ Exits with failure (non-zero) if the timeout is reached before finding the pattern
- 📝 Provides detailed error messages for various failure scenarios
- ⚡ Supports parallel log searching across all pods in a deployment or statefulset

## 📥 Installation

### Prerequisites

- Go 1.24 or higher
- Access to a Kubernetes cluster

### Building from Source

```bash
# Clone the repository
git clone https://github.com/rogosprojects/klogs-needle.git
cd klogs-needle

# Build the binary
go build -o klogs-needle

# Build with version information
go build -ldflags="-X 'main.Version=v1.0.0'" -o klogs-needle
```

### Using Go Install

```bash
go install github.com/rogosprojects/klogs-needle@latest
```

## 🚀 Usage

```bash
klogs-needle [options]

Options:
  -pod string
        Pod name (required if deployment and statefulset not specified)
  -deployment string
        Deployment name (required if pod and statefulset not specified)
  -statefulset string
        StatefulSet name (required if pod and deployment not specified)
  -namespace string
        Kubernetes namespace (default "default")
  -container string
        Container name (optional if pod has only one container)
  -needle string
        Search string/pattern to look for in logs (required)
  -timeout int
        Timeout in seconds (default 60)
  -debug
        Enable debug mode to print logs
  -kubeconfig string
        Path to kubeconfig file (optional, defaults to ~/.kube/config)
  -context string
        Kubernetes context to use (optional)
  -h, -help
        Show help
  -v, -version
        Show version information
```

## 📝 Examples

### Search in a Single Pod

Search for "Service started" in the logs of pod "my-service" in the default namespace with a 60-second timeout:

```bash
klogs-needle -pod my-service -needle "Service started" -timeout 60
```

### Search in a Specific Namespace and Container

```bash
klogs-needle -pod my-pod -namespace my-namespace -container my-container -needle "Initialization complete" -timeout 120
```

### Enable Debug Mode

Enable debug mode to see the logs being monitored:

```bash
klogs-needle -pod my-pod -needle "Ready to accept connections" -timeout 30 -debug
```

### Search in All Pods of a Deployment

```bash
klogs-needle -deployment my-deployment -needle "Service started" -timeout 60
```

With debug mode:

```bash
klogs-needle -deployment my-deployment -namespace my-namespace -needle "Initialization complete" -timeout 120 -debug
```

### Search in All Pods of a StatefulSet

```bash
klogs-needle -statefulset my-statefulset -needle "Service started" -timeout 60
```

With debug mode:

```bash
klogs-needle -statefulset my-statefulset -namespace my-namespace -needle "Initialization complete" -timeout 120 -debug
```

### Using Outside a Kubernetes Cluster

When running outside a Kubernetes cluster, you can specify a kubeconfig file and context:

```bash
# Use default kubeconfig with default context
klogs-needle -pod my-pod -needle "Service started"

# Specify a custom kubeconfig file
klogs-needle -pod my-pod -kubeconfig /path/to/kubeconfig -needle "Service started"

# Specify a specific Kubernetes context
klogs-needle -deployment my-deployment -context production -needle "Service started"
```

## ⚙️ Configuration

klogs-needle is configured through command-line arguments. Here's a detailed explanation of each option:

| Option | Description | Default | Required |
|--------|-------------|---------|----------|
| `-pod` | Pod name to search logs in | - | Yes (if deployment and statefulset not specified) |
| `-deployment` | Deployment name to search logs in all pods | - | Yes (if pod and statefulset not specified) |
| `-statefulset` | StatefulSet name to search logs in all pods | - | Yes (if pod and deployment not specified) |
| `-namespace` | Kubernetes namespace | `default` | No |
| `-container` | Container name | - | No (required if pod has multiple containers) |
| `-needle` | Search string/pattern to look for in logs | - | Yes |
| `-timeout` | Timeout in seconds | `60` | No |
| `-debug` | Enable debug mode to print logs | `false` | No |
| `-kubeconfig` | Path to kubeconfig file | `~/.kube/config` | No |
| `-context` | Kubernetes context to use | - | No |
| `-h`, `-help` | Show help | `false` | No |
| `-v`, `-version` | Show version information | `false` | No |

## 🚦 Exit Codes

klogs-needle uses the following exit codes to indicate the result of execution:

| Code | Description |
|------|-------------|
| 0 | Success - pattern found in logs |
| 1 | Invalid arguments or configuration |
| 2 | Error during execution (pod not found, container not found, connection issues) |
| 3 | Timeout - pattern not found within the specified timeout period |

## 🛠️ Running Inside or Outside Kubernetes

This application can run both inside and outside a Kubernetes cluster:

### Running Inside a Kubernetes Cluster

When running inside a Kubernetes cluster, the application automatically uses the in-cluster configuration. Make sure the pod running this application has appropriate RBAC permissions to read logs from the target pods.

### Running Outside a Kubernetes Cluster

When running outside a Kubernetes cluster, the application automatically detects this and uses your local kubeconfig file. You can specify a custom kubeconfig file path or Kubernetes context.

### Example Kubernetes Job

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: verify-deployment
spec:
  template:
    spec:
      containers:
      - name: klogs-needle
        image: yourusername/klogs-needle:latest
        args:
        - "-deployment"
        - "my-app"
        - "-namespace"
        - "my-namespace"
        - "-needle"
        - "Application started successfully"
        - "-timeout"
        - "300"
      restartPolicy: Never
      serviceAccountName: log-reader-sa
  backoffLimit: 0
```

### Required RBAC Permissions

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: log-reader-sa
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: log-reader
  namespace: default
rules:
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps"]
  resources: ["deployments", "statefulsets"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: log-reader-binding
  namespace: default
subjects:
- kind: ServiceAccount
  name: log-reader-sa
  namespace: default
roleRef:
  kind: Role
  name: log-reader
  apiGroup: rbac.authorization.k8s.io
```

## 👥 Contributing

Contributions are welcome! Here's how you can contribute:

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-new-feature`
3. Commit your changes: `git commit -am 'Add some feature'`
4. Push to the branch: `git push origin feature/my-new-feature`
5. Submit a pull request

### Development Guidelines

- Follow Go best practices and coding standards
- Add tests for new features
- Update documentation as needed
- Make sure all tests pass before submitting a pull request

