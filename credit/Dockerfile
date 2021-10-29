# FROM hub.artifactory.gcp.anz/golang:1.16.4
FROM golang:1.16.4

# ENV GOPROXY https://artifactory.gcp.anz/artifactory/go

WORKDIR /src
COPY . .
RUN go install -mod vendor ./main.go

EXPOSE 80
CMD [ "/go/bin/main" ] 
