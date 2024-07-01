#!/usr/bin/env bash

cd generated && buf generate buf.build/codefly-dev/proto --include-imports &&  goimports -w .

# Fix stupid python imports
#find . -name "*.py" -exec sed -i -e 's/from \([a-zA-Z0-9_\.]*\)\.v0/from codefly_cli.\1.v0/g' {} \;
