# Build em duas etapas, mesmo espírito do resto do repo: a primeira tem
# toda a toolchain do Node, a segunda só carrega o output "standalone" do
# Next.js (server.js + node_modules mínimos que ele próprio já resolve) --
# a imagem final não leva o código-fonte nem devDependencies.
FROM node:20-alpine AS builder

WORKDIR /src
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
RUN npm run build

FROM node:20-alpine
WORKDIR /app
ENV NODE_ENV=production
# O server.js do Next escuta no IP do hostname do container -- com o portal
# em mais de uma rede, a porta publicada pode chegar pela outra e ficar
# pendurada. 0.0.0.0: todas as interfaces.
ENV HOSTNAME=0.0.0.0
COPY --from=builder /src/public ./public
COPY --from=builder /src/.next/standalone ./
COPY --from=builder /src/.next/static ./.next/static

EXPOSE 3000
CMD ["node", "server.js"]
