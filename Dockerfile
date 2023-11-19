FROM alpine:latest 
RUN apk add -U --no-cache ca-certificates && rm -rf /var/cache/apk/*
ADD streamer /
EXPOSE 3000
ENTRYPOINT ["/streamer"]