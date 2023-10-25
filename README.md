![workflow](https://github.com/codefly-dev/core/actions/workflows/go.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/codefly-dev/core)](https://goreportcard.com/report/github.com/codefly-dev/core)
[![Go Reference](https://pkg.go.dev/badge/github.com/codefly-dev/core.svg)](https://pkg.go.dev/github.com/codefly-dev/sdk-go)
![coverage](https://raw.githubusercontent.com/codefly-dev/core/badges/.badges/main/coverage.svg)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)


![](docs/media/dragonfly.png)

# Welcome to `codefly.ai` core library

What is this?

## Shared code

This repository contains the shared code for the CodeFly ecosystem.

## Configuration definition and helpers

Configurations are hierarchical. We use the terminology of reference: a Project has ApplicationReferences.

A reference contains just enough data to load the associated configuration.

### Project

A folder is a project folder if it contains a `project.codefly.yaml` file.



## Templating on steroids

Within `codefly.ai`, we do *a lot* of templating. We add some powerful tools.