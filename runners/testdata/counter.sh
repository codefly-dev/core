#!/usr/bin/env sh
counter=0
while true; do
  echo $counter
  counter=$(expr $counter + 1)
  sleep 0.1
done
