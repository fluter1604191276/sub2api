<template>
  <div class="relative inline-block">
    <span
      ref="triggerEl"
      :class="[
        'inline-flex cursor-help items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium transition-colors',
        effectivePlatform
          ? platformBadgeClass(effectivePlatform)
          : 'border-gray-200 bg-gray-50 text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300',
      ]"
      @mouseenter="setTriggerHovered(true)"
      @mouseleave="setTriggerHovered(false)"
      @focusin="setTriggerFocused(true)"
      @focusout="setTriggerFocused(false)"
      @keydown.esc="closePopover"
      tabindex="0"
    >
      <PlatformIcon
        v-if="effectivePlatform"
        :platform="effectivePlatform as GroupPlatform"
        size="xs"
      />
      <span
        v-if="showPlatform && model.platform"
        class="rounded bg-gray-200/60 px-1 text-[10px] uppercase text-gray-600 dark:bg-dark-700 dark:text-gray-400"
      >
        {{ model.platform }}
      </span>
      {{ model.name }}
    </span>

    <!-- Teleport to body so the popover is not clipped by card/overflow-hidden
         ancestors. Fixed-position coords are computed from the trigger's
         bounding rect; re-measured on enter / scroll / resize. -->
    <Teleport to="body">
      <div
        v-show="show"
        ref="popoverEl"
        role="tooltip"
        tabindex="0"
        class="pointer-events-auto fixed z-[99999] max-h-[calc(100vh-1rem)] w-80 max-w-[min(22rem,calc(100vw-1rem))] overflow-y-auto rounded-lg border bg-white text-xs shadow-xl dark:bg-dark-800"
        :class="[popoverBorderClass]"
        :style="popoverStyle"
        @mouseenter="setPopoverHovered(true)"
        @mouseleave="setPopoverHovered(false)"
        @focusin="setPopoverFocused(true)"
        @focusout="setPopoverFocused(false)"
        @keydown.esc="closePopover"
      >
        <!-- Header：平台主题色背景，含模型名 + 平台徽章 -->
        <div
          class="flex items-center justify-between gap-2 rounded-t-lg border-b px-3 py-2"
          :class="[popoverHeaderClass, popoverBorderClass]"
        >
          <span class="truncate font-semibold">{{ model.name }}</span>
          <span
            v-if="model.platform"
            class="flex-shrink-0 rounded bg-white/70 px-1.5 py-0.5 text-[10px] uppercase tracking-wide dark:bg-dark-900/60"
          >
            {{ model.platform }}
          </span>
        </div>

        <div class="p-3">
          <div v-if="!model.pricing" class="text-gray-500 dark:text-gray-400">
            {{ noPricingLabel }}
          </div>

          <div v-else class="space-y-3 text-gray-700 dark:text-gray-300">
            <section
              v-for="(context, contextIndex) in displayPricingContexts"
              :key="context.key"
              :class="contextIndex > 0 ? 'border-t pt-3' : ''"
            >
            <div
              v-if="context.heading"
              class="mb-2 flex items-center justify-between gap-2 border-b pb-2 font-medium text-gray-600 dark:text-gray-400"
              :class="[popoverBorderClass]"
            >
              <span>{{ context.heading }}</span>
              <span v-if="context.multiplierLabel" class="font-mono text-[11px] text-gray-400 dark:text-dark-500">{{ context.multiplierLabel }}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-gray-500 dark:text-gray-400">{{ t(prefixKey('billingMode')) }}</span>
              <span>{{ billingModeLabel(context.pricing) }}</span>
            </div>

            <template v-if="context.pricing.billing_mode === BILLING_MODE_TOKEN">
              <PricingRow
                :label="t(prefixKey('inputPrice'))"
                :value="context.pricing.input_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
              <PricingRow
                :label="t(prefixKey('outputPrice'))"
                :value="context.pricing.output_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
              <PricingRow
                :label="t(prefixKey('cacheWrite5mPrice'))"
                :value="context.pricing.cache_write_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
              <PricingRow
                v-if="context.pricing.cache_write_1h_price != null"
                :label="t(prefixKey('cacheWrite1hPrice'))"
                :value="context.pricing.cache_write_1h_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
              <PricingRow
                :label="t(prefixKey('cacheReadPrice'))"
                :value="context.pricing.cache_read_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
              <PricingRow
                v-if="context.pricing.image_input_price != null"
                :label="t(prefixKey('imageInputPrice'))"
                :value="context.pricing.image_input_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
              <PricingRow
                v-if="context.pricing.image_output_price != null"
                :label="t(prefixKey('imageOutputPrice'))"
                :value="context.pricing.image_output_price"
                :unit="t(prefixKey('unitPerMillion'))"
                :scale="perMillionScale"
                :currency="context.pricing.currency"
              />
            </template>

            <PricingRow
              v-if="
                context.pricing.billing_mode === BILLING_MODE_PER_REQUEST &&
                context.pricing.per_request_price != null
              "
              :label="t(prefixKey('perRequestPrice'))"
              :value="context.pricing.per_request_price"
              :unit="t(prefixKey('unitPerRequest'))"
              :scale="1"
              :currency="context.pricing.currency"
            />

            <PricingRow
              v-if="
                context.pricing.billing_mode === BILLING_MODE_IMAGE &&
                (context.pricing.per_request_price != null || context.pricing.image_output_price != null)
              "
              :label="t(prefixKey('imageOutputPrice'))"
              :value="context.pricing.per_request_price ?? context.pricing.image_output_price"
              :unit="t(prefixKey('unitPerRequest'))"
              :scale="1"
              :currency="context.pricing.currency"
            />

            <div
              v-if="context.pricing.intervals && context.pricing.intervals.length > 0"
              class="mt-2 border-t pt-2"
              :class="[popoverBorderClass]"
            >
              <div class="mb-1 font-medium text-gray-600 dark:text-gray-400">
                {{ t(prefixKey('intervals')) }}
              </div>
              <div class="space-y-1">
                <div
                  v-for="(iv, idx) in context.pricing.intervals"
                  :key="idx"
                  class="flex justify-between text-[11px]"
                >
                  <span class="text-gray-500 dark:text-gray-400">
                    <template v-if="iv.tier_label">{{ iv.tier_label }}</template>
                    <template v-else>{{ formatRange(iv.min_tokens, iv.max_tokens) }}</template>
                  </span>
                  <span>{{ formatInterval(iv, context.pricing) }}</span>
                </div>
              </div>
            </div>
            </section>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PricingRow from './PricingRow.vue'
import { formatScaled, resolveIntervalPrices } from '@/utils/pricing'
import {
  BILLING_MODE_TOKEN,
  BILLING_MODE_PER_REQUEST,
  BILLING_MODE_IMAGE
} from '@/constants/channel'
// 复用 api/channels.ts 的用户侧最小形态 DTO。
// admin 侧 ChannelModelPricing 字段更多，但结构上是用户 DTO 的超集，admin 视图传入可直接通过结构化子类型检查。
import type { UserPricingInterval, UserSupportedModel, UserSupportedModelPricing } from '@/api/channels'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import { platformBadgeClass, platformBorderClass, platformBadgeLightClass } from '@/utils/platformColors'

const props = withDefaults(
  defineProps<{
    model: UserSupportedModel
    /** i18n 前缀：管理端传 `admin.availableChannels.pricing`，用户端传 `availableChannels.pricing`。 */
    pricingKeyPrefix?: string
    noPricingLabel?: string
    /** Optional pricing context for user-facing surfaces; omitted by admin callers. */
    pricingHeading?: string
    pricingContexts?: Array<{
      key: string
      heading: string
      multiplierLabel?: string
      pricing: UserSupportedModelPricing
    }>
    showPlatform?: boolean
    /**
     * 当 model.platform 缺失（如 admin 聚合场景）时，用父行的平台作为兜底着色。
     * 仅用于视觉，不影响业务逻辑。
     */
    platformHint?: string
  }>(),
  {
    pricingKeyPrefix: 'availableChannels.pricing',
    noPricingLabel: '',
    pricingHeading: '',
    showPlatform: true,
    platformHint: ''
  }
)

const effectivePlatform = computed<string>(() => props.model.platform || props.platformHint || '')

const { t } = useI18n()

const displayPricingContexts = computed(() => props.pricingContexts?.length
  ? props.pricingContexts
  : props.model.pricing
    ? [{ key: 'default', heading: props.pricingHeading, pricing: props.model.pricing }]
    : [])

/** 按 token 定价展示时的换算单位：每百万 token。 */
const perMillionScale = 1_000_000

// Popover border + header classes echo the platform theme so each card reads
// at a glance which model family it belongs to.
const popoverBorderClass = computed(() =>
  effectivePlatform.value
    ? platformBorderClass(effectivePlatform.value)
    : 'border-gray-200 dark:border-dark-600',
)
const popoverHeaderClass = computed(() =>
  effectivePlatform.value
    ? platformBadgeLightClass(effectivePlatform.value)
    : 'bg-gray-50 text-gray-700 dark:bg-dark-700/60 dark:text-gray-300',
)

function prefixKey(k: string): string {
  return `${props.pricingKeyPrefix}.${k}`
}

function billingModeLabel(pricing: UserSupportedModelPricing): string {
  const mode = pricing.billing_mode
  switch (mode) {
    case BILLING_MODE_TOKEN:
      return t(prefixKey('billingModeToken'))
    case BILLING_MODE_PER_REQUEST:
      return t(prefixKey('billingModePerRequest'))
    case BILLING_MODE_IMAGE:
      return t(prefixKey('billingModeImage'))
    default:
      return '-'
  }
}

function formatRange(min: number, max: number | null): string {
  const maxLabel = max == null ? '∞' : String(max)
  return `(${min}, ${maxLabel}]`
}

function formatInterval(iv: UserPricingInterval, pricing: UserSupportedModelPricing): string {
  if (pricing.billing_mode === BILLING_MODE_PER_REQUEST || pricing.billing_mode === BILLING_MODE_IMAGE) {
    return formatScaled(iv.per_request_price, 1, 0, pricing.currency)
  }
  const resolved = resolveIntervalPrices(iv, pricing)
  const input = formatScaled(resolved.input_price, perMillionScale, 0, pricing.currency)
  const output = formatScaled(resolved.output_price, perMillionScale, 0, pricing.currency)
  return `${input} / ${output}`
}

// ── Popover positioning ─────────────────────────────────────────────
// Teleport-to-body + fixed positioning avoids being clipped by
// overflow-hidden ancestors (the parent table card). We re-measure on
// hover enter, scroll, and resize. Pinning to the trigger's top-center
// with a flip when the viewport edge is near keeps it aligned without a
// full-blown positioning lib.
const show = ref(false)
const triggerEl = ref<HTMLElement | null>(null)
const popoverEl = ref<HTMLElement | null>(null)
const popoverStyle = ref<Record<string, string>>({ top: '0px', left: '0px' })
let closeTimer: ReturnType<typeof setTimeout> | null = null
let triggerHovered = false
let triggerFocused = false
let popoverHovered = false
let popoverFocused = false

function updatePosition() {
  const trigger = triggerEl.value
  if (!trigger) return
  const rect = trigger.getBoundingClientRect()
  const margin = 8
  const popover = popoverEl.value
  const popWidth = popover?.offsetWidth ?? 320
  const popHeight = popover?.offsetHeight ?? 240
  const vw = window.innerWidth
  const vh = window.innerHeight

  let top = rect.bottom + margin
  // Flip upward if it would overflow below.
  if (top + popHeight > vh - margin) {
    top = Math.max(margin, rect.top - popHeight - margin)
  }

  let left = rect.left + rect.width / 2 - popWidth / 2
  if (left < margin) left = margin
  if (left + popWidth > vw - margin) left = vw - margin - popWidth

  popoverStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
  }
}

function openPopover() {
  if (closeTimer) {
    clearTimeout(closeTimer)
    closeTimer = null
  }
  show.value = true
  nextTick(() => {
    updatePosition()
    window.addEventListener('scroll', updatePosition, true)
    window.addEventListener('resize', updatePosition)
  })
}

function scheduleClose() {
  if (closeTimer) clearTimeout(closeTimer)
  closeTimer = setTimeout(() => {
    if (!triggerHovered && !triggerFocused && !popoverHovered && !popoverFocused) {
      closePopover()
    }
  }, 100)
}

function setTriggerHovered(value: boolean) {
  triggerHovered = value
  value ? openPopover() : scheduleClose()
}

function setTriggerFocused(value: boolean) {
  triggerFocused = value
  value ? openPopover() : scheduleClose()
}

function setPopoverHovered(value: boolean) {
  popoverHovered = value
  value ? openPopover() : scheduleClose()
}

function setPopoverFocused(value: boolean) {
  popoverFocused = value
  value ? openPopover() : scheduleClose()
}

function closePopover() {
  if (closeTimer) {
    clearTimeout(closeTimer)
    closeTimer = null
  }
  show.value = false
  window.removeEventListener('scroll', updatePosition, true)
  window.removeEventListener('resize', updatePosition)
}

onBeforeUnmount(() => {
  if (closeTimer) clearTimeout(closeTimer)
  window.removeEventListener('scroll', updatePosition, true)
  window.removeEventListener('resize', updatePosition)
})
</script>
