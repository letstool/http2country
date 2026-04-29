#!/bin/bash

IMAGE_TAG=letstool/http2country:latest

docker build \
        -t "$IMAGE_TAG" \
       -f build/Dockerfile \
       .
