FROM node:22-alpine AS frontend-builder

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY index.html tsconfig.json tsconfig.app.json tsconfig.node.json vite.config.ts eslint.config.js ./
COPY public ./public
COPY src ./src

RUN npm run build


FROM golang:1.25-alpine AS backend-builder

WORKDIR /app/backend

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend ./

RUN go build -o /app/sql-seminar-backend .


FROM alpine:3.22

WORKDIR /app/backend

RUN addgroup -S app && adduser -S app -G app \
  && mkdir -p /app/server-data \
  && chown -R app:app /app

COPY --from=frontend-builder /app/dist /app/dist
COPY --from=backend-builder /app/sql-seminar-backend /app/backend/sql-seminar-backend
COPY backend/seed.json /app/backend/seed.json

ENV PORT=3001
ENV JWT_SECRET=change-me

EXPOSE 3001

USER app

CMD ["/app/backend/sql-seminar-backend"]
