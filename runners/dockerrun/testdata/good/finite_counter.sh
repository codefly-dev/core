#!/usr/bin/env sh
counter=0
while true; do
  echo $counter
  counter=$(expr $counter + 1)
  if [ $counter -gt 5 ]; then
    break
  fi
  sleep 0.01
done
