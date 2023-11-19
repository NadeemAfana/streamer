# Realtime File Streamer
Share a file in real time by using an HTTP URL. 
Built using plain Go framework and has no external dependencies. 

Use cases:
1.  To pull files from a machine behind a firewall where you only have command line access.
2. To pull file from a Docker container without additional mounting or configuration.
3. To share a sensitive file with someone without uploading it to the Internet.
3. Self-hosted which means it provides better security and trust.

## How it Works
Simply upload a file and then share the download link in the response.
```
curl -i  -X POST -u "user:password" -T hello.txt http://localhost:3000/streamer/
```
The response will look like
```
To download the file, curl -o hello.txt http://localhost:3000/streamer/DOMgNFnP1j7Nw__7LO8pR6Xl46oDqPuQqkTN2-FbmiSu2ie5
```

As soon as the download link is opened, the file will be sent:

```
Client connected.
hello.txt was transferred successfully.
```

## Setup
The http service must be hosted.

The following environment variables are needed to run the service:
1. `DOWNLOAD_BASE_URL`: The base URL where the service is hosted and which contains the download links. For localhost, use `http://localhost:3000`.
2. `PORT`: Http listening port for the service.
3. `USER_NAME`: HTTP Basic Auth user name. This only allows certian users to use the service.
4. `USER_PASSWORD`: HTTP Basic Auth user password. This only allows certian users to use the service.

To run the service locally:

```
go build

DOWNLOAD_BASE_URL="http://localhost:3000" PORT=3000 USER_NAME=user USER_PASSWORD=password  ./streamer
```

To run the service using Docker:
```
CGO_ENABLED=0 go build
docker build . -t=streamer
docker run -p 3000:3000 -e DOWNLOAD_BASE_URL="http://localhost:3000" -e PORT=3000 -e USER_NAME=user -e USER_PASSWORD=password streamer
```



