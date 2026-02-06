#!/usr/bin/env bash

# Generate proto code for codefly core
# Usage:
#   ./generate.sh          # Generate from remote buf.build/codefly-dev/proto
#   ./generate.sh --local  # Generate from local ../proto folder

set -e

cd "$(dirname "$0")/../../generated"

if [ "$1" == "--local" ]; then
    echo "Generating from local proto folder..."
    buf generate ../../proto
else
    echo "Generating from buf.build/codefly-dev/proto..."
    buf generate buf.build/codefly-dev/proto
fi

# Format Go code
if command -v goimports &> /dev/null; then
    goimports -w .
fi

echo "Proto generation complete!"

# Fix stupid python imports (commented out - may not be needed)
#find . -name "*.py" -exec sed -i -e 's/from \([a-zA-Z0-9_\.]*\)\.v0/from codefly_cli.\1.v0/g' {} \;
