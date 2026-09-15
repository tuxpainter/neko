<template>
  <li class="hls-output">
    <div class="heading">
      <span>{{ $t('setting.hls.title') }}</span>
      <small :class="{ active: status.is_active && status.ready }">
        {{
          status.is_active
            ? status.ready
              ? $t('setting.hls.ready')
              : $t('setting.hls.starting')
            : $t('setting.hls.inactive')
        }}
      </small>
    </div>
    <p class="description">{{ $t('setting.hls.description') }}</p>
    <label class="quality-label" for="hls-quality">{{ $t('setting.hls.quality') }}</label>
    <select id="hls-quality" v-model="quality" :disabled="busy" aria-describedby="hls-description">
      <option value="480p">{{ $t('setting.hls.480p') }}</option>
      <option value="720p">{{ $t('setting.hls.720p') }}</option>
      <option value="1080p">{{ $t('setting.hls.1080p') }}</option>
    </select>
    <p id="hls-description" class="help">{{ $t('setting.hls.help') }}</p>
    <input v-if="url" class="url" :value="url" readonly aria-label="HLS playback URL" @focus="$event.target.select()" />
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <div class="actions">
      <button v-if="!status.is_active" :disabled="busy" @click="create">{{ $t('setting.hls.create') }}</button>
      <button v-else :disabled="busy" @click="recreate">{{ $t('setting.hls.recreate') }}</button>
      <button v-if="url" :disabled="busy" @click="copy">{{ $t('setting.hls.copy') }}</button>
      <button v-if="status.is_active" class="btn-red" :disabled="busy" @click="revoke">
        {{ $t('setting.hls.revoke') }}
      </button>
    </div>
  </li>
</template>

<style lang="scss" scoped>
  .hls-output {
    display: block !important;
    white-space: normal !important;
  }
  .heading {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  .heading small {
    color: $text-muted;
    font-size: 11px;
    text-transform: uppercase;
  }
  .heading small.active {
    color: $style-primary;
  }
  .description,
  .help {
    color: $text-muted;
    font-size: 12px;
    line-height: 1.4;
    margin: 5px 0 10px;
    white-space: normal;
  }
  .quality-label {
    display: block;
    font-size: 12px;
    margin-bottom: 4px;
  }
  select,
  .url {
    box-sizing: border-box;
    width: 100%;
    height: 30px;
    padding: 0 8px;
    border: 1px solid transparent;
    border-radius: 5px;
    color: white;
    background: $background-tertiary;
  }
  .url {
    margin-top: 5px;
    user-select: auto;
  }
  .actions {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-top: 5px;
  }
  .actions button {
    width: auto !important;
    flex: 1 1 auto;
    padding: 3px 8px;
    margin: 0 !important;
  }
  .actions .btn-red {
    background: #a62626;
  }
  .error {
    color: #ff8c8c;
    font-size: 12px;
    white-space: normal;
  }
</style>

<script lang="ts">
  import { Component, Vue, Watch } from 'vue-property-decorator'

  type Quality = '480p' | '720p' | '1080p'
  interface HlsStatus {
    is_active: boolean
    quality: Quality
    playback_path?: string
    ready: boolean
    message?: string
  }

  @Component({ name: 'neko-hls-output' })
  export default class extends Vue {
    quality: Quality = '720p'
    status: HlsStatus = { is_active: false, quality: '720p', ready: false }
    busy = false
    error = ''
    timer: number | null = null

    get apiToken(): string {
      return this.$accessor.user.api_token
    }

    get url(): string {
      if (!this.status.playback_path) return ''
      try {
        const base = new URL(this.$http.defaults.baseURL || window.location.origin, window.location.origin)
        return new URL(this.status.playback_path, base.origin).toString()
      } catch (_) {
        return ''
      }
    }

    mounted() {
      this.refresh()
      this.timer = window.setInterval(() => this.refresh(), 10000)
    }
    beforeDestroy() {
      if (this.timer !== null) window.clearInterval(this.timer)
    }

    async refresh() {
      if (this.busy || !this.apiToken || !this.$accessor.connected) return
      try {
        const response = await this.$http.get<HlsStatus>('api/hls/', {
          headers: { Authorization: `Bearer ${this.apiToken}` },
        })
        this.status = response.data
        if (response.data.message) this.error = response.data.message
        else this.error = ''
      } catch (error) {
        const status = error && (error as any).response && (error as any).response.status
        if (status === 403) this.error = this.$t('setting.hls.forbidden').toString()
        else if (status !== 401) this.error = this.$t('setting.hls.load_error').toString()
      }
    }

    async create() {
      await this.change()
    }
    async recreate() {
      if (window.confirm(this.$t('setting.hls.recreate_confirm').toString())) await this.change()
    }
    async revoke() {
      if (!window.confirm(this.$t('setting.hls.revoke_confirm').toString())) return
      this.busy = true
      try {
        const response = await this.$http.delete<HlsStatus>('api/hls/', {
          headers: { Authorization: `Bearer ${this.apiToken}` },
        })
        this.status = response.data
        this.error = response.data.message || ''
      } catch (_) {
        this.error = this.$t('setting.hls.action_error').toString()
      } finally {
        this.busy = false
      }
    }
    async change() {
      this.busy = true
      try {
        const response = await this.$http.post<HlsStatus>(
          'api/hls/',
          { quality: this.quality },
          { headers: { Authorization: `Bearer ${this.apiToken}` } },
        )
        this.status = response.data
        this.error = response.data.message || ''
      } catch (_) {
        this.error = this.$t('setting.hls.action_error').toString()
      } finally {
        this.busy = false
      }
    }
    async copy() {
      try {
        await navigator.clipboard.writeText(this.url)
      } catch (_) {
        const input = this.$el.querySelector('.url') as HTMLInputElement
        input.focus()
        input.select()
        this.error = this.$t('setting.hls.copy_fallback').toString()
      }
    }

    @Watch('apiToken')
    onApiTokenChange(token: string) {
      if (token) this.refresh()
    }
  }
</script>
