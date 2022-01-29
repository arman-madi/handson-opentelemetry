FROM golang:1.16.4

WORKDIR /src
COPY go.mod .
COPY go.sum .
RUN go mod download

COPY main.go .
RUN go install ./main.go

EXPOSE 80
CMD [ "/go/bin/main" ] 
