#!/usr/bin/env bash

for lang in go node proto python-poetry; do
    ./companions/$lang/scripts/tag.sh
    ./companions/$lang/scripts/build_companion.sh
    ./companions/$lang/scripts/push_companion.sh
done
