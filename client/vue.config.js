const path = require('path')
const fs = require('fs')

const tlsCert = process.env.NEKO_CLIENT_TLS_CERT
const tlsKey = process.env.NEKO_CLIENT_TLS_KEY

module.exports = {
  productionSourceMap: false,
  css: {
    loaderOptions: {
      sass: {
        additionalData: `
          @import "@/assets/styles/_variables.scss";
        `,
      },
    },
  },
  publicPath: './',
  assetsDir: './',
  configureWebpack: {
    resolve: {
      alias: {
        vue$: 'vue/dist/vue.esm.js',
        '~': path.resolve(__dirname, 'src/'),
      },
    },
  },
  devServer: {
    allowedHosts: 'all',
    ...(tlsCert && tlsKey
      ? {
          server: {
            type: 'https',
            options: {
              cert: fs.readFileSync(tlsCert),
              key: fs.readFileSync(tlsKey),
            },
          },
        }
      : {}),
  },
}
