FROM golang:1.16.4

WORKDIR /src
COPY go.* .
RUN go mod download

COPY main.go .
RUN go install ./main.go

EXPOSE 80
CMD [ "/go/bin/main" ] 
