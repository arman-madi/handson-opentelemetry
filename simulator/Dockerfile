# FROM curlimages/curl:7.79.1 
FROM bash:devel-alpine3.14

RUN apk add curl

COPY entrypoint /bin/entrypoint

CMD [ "bash", "/bin/entrypoint" ]