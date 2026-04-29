@echo off
go build ^
    -trimpath ^
    -ldflags="-s -w" ^
    -o .\out\http2country.exe .\cmd\http2country
