FROM ruby:3.3.1-alpine

# Install essential build tools and libraries
RUN apk add --update --no-cache \
    build-base \
    tzdata \
    postgresql-dev \
    nodejs \
    yarn

RUN gem install rails

WORKDIR /codefly
