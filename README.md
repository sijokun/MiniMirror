# TonMirror
Simple and lightweight Go microservice to mirror a website.

## Dev

`go run TonMirror`

## Production

`go build`

`./MiniMirror`


## Production in Docker
`docker run -it -p 3000:3000 $(docker build -q .)`

## Configuration 
Configuration is set via CONFIG env variable, config is in JSON format.

### Example config
```json
{
  "127.0.0.1:3000": { 
    "domain": "https://example.com", 
    "target": "https://example.com",
    "secondary_domains": ["https://something.example.com"]
  }
}
```