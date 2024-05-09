#!/usr/bin/env bash

cd generated && buf generate buf.build/codefly-dev/proto --include-imports &&  goimports -w .
