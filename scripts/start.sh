#!/bin/bash
if [[ $DYNO == "web"* ]]; then
    /app/bin/web
elif  [[ $DYNO == "worker"* ]]; then
    /app/bin/worker
fi