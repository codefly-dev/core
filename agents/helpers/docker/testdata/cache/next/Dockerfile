FROM node:22-alpine AS dependencies
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
FROM dependencies AS build
COPY pages ./pages
COPY next.config.js ./
ENV NEXT_TELEMETRY_DISABLED=1
ARG BUILD_INPUT=one
RUN NEXT_PUBLIC_BUILD_INPUT=${BUILD_INPUT} npm run build
FROM scratch
COPY --from=build /app/out /out
