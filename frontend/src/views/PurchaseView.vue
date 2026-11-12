<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import {
  NSelect, NRadio, NRadioGroup, NButton, NInputNumber,
  NTag, NSpace, NDivider, NModal, NSpin, NEmpty,
  NResult, useMessage
} from 'naive-ui'
import { resource, rows, unwrap, errorText, orderApi, type RecordData } from '../api'

const message = useMessage()

// ============================================================
// 状态
// ============================================================
const loading = ref(false)
const submitting = ref(false)
const orderSuccess = ref(false)
const createdOrderId = ref<number | null>(null)

// 产品列表（从 API 获取）
const products = ref<RecordData[]>([])
// 机房列表（用于地域选择）
const datacenters = ref<RecordData[]>([])
// 镜像列表（用于操作系统选择）
const images = ref<RecordData[]>([])

// 用户选择
const selectedDatacenter = ref<string | null>(null)
const selectedProductId = ref<string | null>(null)
const selectedImageId = ref<string | null>(null)
const bandwidthMbps = ref(100)
const ipCount = ref(1)
const duration = ref('1month')

// 优惠券
const couponCode = ref('')
const couponApplied = ref(false)
const couponDiscount = ref(0)

// 订单确认弹窗
const showOrderModal = ref(false)

// ============================================================
// 购买时长选项
// ============================================================
const durationOptions = [
  { value: '1month', label: '1个月', months: 1, discount: 100, badge: '' },
  { value: '3months', label: '3个月', months: 3, discount: 98, badge: '98折' },
  { value: '6months', label: '6个月', months: 6, discount: 92, badge: '92折' },
  { value: '1year', label: '1年', months: 12, discount: 85, badge: '85折' },
  { value: '2years', label: '2年', months: 24, discount: 78, badge: '78折' },
  { value: '3years', label: '3年', months: 36, discount: 70, badge: '7折' },
]

// ============================================================
// 计算属性
// ============================================================
const selectedProduct = computed(() => {
  return products.value.find(p => String(p.id) === selectedProductId.value) || null
})

const selectedDatacenterObj = computed(() => {
  return datacenters.value.find(d => String(d.id) === selectedDatacenter.value) || null
})

const selectedImage = computed(() => {
  return images.value.find(i => String(i.id) === selectedImageId.value) || null
})

const selectedDuration = computed(() => {
  return durationOptions.find(d => d.value === duration.value) || durationOptions[0]
})

// 解析产品定价
function parseProductPricing(product: RecordData | null): { basePrice: number; cpuPrice: number; memPrice: number; diskPrice: number; bwPrice: number; ipPrice: number } {
  if (!product) return { basePrice: 0, cpuPrice: 0, memPrice: 0, diskPrice: 0, bwPrice: 0, ipPrice: 0 }
  let pricing: any = {}
  try {
    pricing = typeof product.pricing_json === 'string' ? JSON.parse(product.pricing_json) : (product.pricing_json || {})
  } catch { pricing = {} }
  return {
    basePrice: Number(pricing.base_price || pricing.monthly_price || 0),
    cpuPrice: Number(pricing.cpu_price || 0),
    memPrice: Number(pricing.mem_price || pricing.memory_price || 0),
    diskPrice: Number(pricing.disk_price || pricing.storage_price || 0),
    bwPrice: Number(pricing.bw_price || pricing.bandwidth_price || 0),
    ipPrice: Number(pricing.ip_price || pricing.public_ip_price || 0),
  }
}

// 解析产品规格
function parseProductSpec(product: RecordData | null): { cpu: string; ram: string; disk: string; name: string } {
  if (!product) return { cpu: '-', ram: '-', disk: '-', name: '-' }
  let spec: any = {}
  try {
    spec = typeof product.spec_json === 'string' ? JSON.parse(product.spec_json) : (product.spec_json || {})
  } catch { spec = {} }
  return {
    cpu: spec.cpu || spec.processor || '-',
    ram: spec.ram || spec.memory || '-',
    disk: spec.disk || spec.storage || '-',
    name: spec.model || spec.name || product.name || '-',
  }
}

// 单价计算（月价）
const unitPrice = computed(() => {
  const p = parseProductPricing(selectedProduct.value)
  const spec = parseProductSpec(selectedProduct.value)
  // 基础价 + CPU/内存/磁盘附加 + 带宽 + IP
  const cpuCores = parseInt(spec.cpu) || 1
  const ramGb = parseInt(spec.ram) || 1
  const diskGb = parseInt(spec.disk) || 1
  const bwCost = bandwidthMbps.value * p.bwPrice
  const ipCost = ipCount.value * p.ipPrice
  return p.basePrice + (cpuCores * p.cpuPrice) + (ramGb * p.memPrice) + (diskGb * p.diskPrice) + bwCost + ipCost
})

// 折扣率
const discountRate = computed(() => {
  return selectedDuration.value.discount / 100
})

// 折后月价
const discountedMonthly = computed(() => {
  return unitPrice.value * discountRate.value
})

// 总价
const totalPrice = computed(() => {
  const months = selectedDuration.value.months
  let total = discountedMonthly.value * months
  // 优惠券折扣
  if (couponApplied.value) {
    total = total - couponDiscount.value
    if (total < 0) total = 0
  }
  return Math.round(total * 100) / 100
})

// 节省金额
const savedAmount = computed(() => {
  const months = selectedDuration.value.months
  const original = unitPrice.value * months
  const discounted = discountedMonthly.value * months
  return Math.round((original - discounted) * 100) / 100
})

// 地域选项
const datacenterOptions = computed(() => {
  return datacenters.value.map(d => ({
    label: `${d.name}${d.location ? ' · ' + d.location : ''}`,
    value: String(d.id)
  }))
})

// 产品卡片分组（按规格分类展示）
const productCards = computed(() => {
  return products.value.filter(p => p.status === 'active')
})

// 操作系统选项
const imageOptions = computed(() => {
  return images.value.map(img => ({
    label: `${img.name} ${img.version || ''} (${img.architecture || 'x86_64'})`,
    value: String(img.id),
    os: img.os_type,
    icon: getOsIcon(String(img.os_type || ''))
  }))
})

function getOsIcon(osType: string): string {
  const t = (osType || '').toLowerCase()
  if (t.includes('centos') || t.includes('rhel') || t.includes('redhat')) return '🐧'
  if (t.includes('ubuntu') || t.includes('debian')) return '🟠'
  if (t.includes('windows') || t.includes('server')) return '🪟'
  if (t.includes('freebsd')) return '😈'
  return '💿'
}

// ============================================================
// 数据加载
// ============================================================
async function loadProducts() {
  try {
    const res = await resource.list('/products', { status: 'active', page: 1, page_size: 100 })
    const data = rows(res)
    products.value = data.items
    // 默认选中第一个
    if (data.items.length > 0 && !selectedProductId.value) {
      selectedProductId.value = String(data.items[0].id)
    }
  } catch (e) {
    message.error('产品列表加载失败：' + errorText(e))
  }
}

async function loadDatacenters() {
  try {
    const res = await resource.list('/datacenters', { page: 1, page_size: 100 })
    const data = rows(res)
    datacenters.value = data.items
    if (data.items.length > 0 && !selectedDatacenter.value) {
      selectedDatacenter.value = String(data.items[0].id)
    }
  } catch (e) {
    message.error('机房列表加载失败：' + errorText(e))
  }
}

async function loadImages() {
  try {
    const res = await resource.list('/images', { page: 1, page_size: 100 })
    const data = rows(res)
    images.value = data.items
  } catch (e) {
    message.error('镜像列表加载失败：' + errorText(e))
  }
}

async function loadAll() {
  loading.value = true
  await Promise.allSettled([loadProducts(), loadDatacenters(), loadImages()])
  loading.value = false
}

// ============================================================
// 优惠券验证
// ============================================================
async function applyCoupon() {
  if (!couponCode.value.trim()) return
  try {
    const res = await resource.list('/coupons', { search: couponCode.value.trim() })
    const data = rows(res)
    const coupon = data.items.find((c: any) => c.code === couponCode.value.trim() && c.status === 'active')
    if (!coupon) {
      message.warning('优惠券无效或不存在')
      couponApplied.value = false
      couponDiscount.value = 0
      return
    }
    // 计算折扣
    const val = Number(coupon.value || 0)
    if (coupon.type === 'fixed') {
      couponDiscount.value = val
    } else if (coupon.type === 'percent') {
      couponDiscount.value = totalPrice.value * val / 100
    }
    couponApplied.value = true
    message.success('优惠券已应用')
  } catch {
    message.error('优惠券验证失败')
    couponApplied.value = false
  }
}

// ============================================================
// 下单
// ============================================================
function openOrderModal() {
  if (!selectedProduct.value) {
    message.warning('请先选择服务器配置')
    return
  }
  if (!selectedDatacenter.value) {
    message.warning('请先选择地域机房')
    return
  }
  showOrderModal.value = true
}

async function submitOrder() {
  submitting.value = true
  try {
    const payload: RecordData = {
      type: 'purchase',
      product_id: Number(selectedProductId.value),
      amount: unitPrice.value * selectedDuration.value.months,
      discount_amount: savedAmount.value + (couponApplied.value ? couponDiscount.value : 0),
    }
    if (couponCode.value.trim() && couponApplied.value) {
      payload.coupon_code = couponCode.value.trim()
    }
    const res = await orderApi.create('/orders', payload)
    const data = unwrap<{ id?: number | string }>(res)
    createdOrderId.value = data.id ? Number(data.id) : null
    orderSuccess.value = true
    message.success('订单创建成功！')
  } catch (e) {
    message.error('订单创建失败：' + errorText(e))
  } finally {
    submitting.value = false
  }
}

function closeOrderModal() {
  showOrderModal.value = false
  if (orderSuccess.value) {
    // 重置表单
    orderSuccess.value = false
    createdOrderId.value = null
    couponCode.value = ''
    couponApplied.value = false
    couponDiscount.value = 0
  }
}

// ============================================================
// 格式化
// ============================================================
function formatPrice(val: number): string {
  return '¥' + val.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// ============================================================
// 生命周期
// ============================================================
onMounted(() => {
  loadAll()
})

// 监听产品变化时重置镜像选择
watch(selectedProductId, () => {
  selectedImageId.value = null
})
</script>

<template>
  <div class="page purchase-page">
    <!-- 页面标题 -->
    <div class="page-head">
      <div>
        <div class="eyebrow dark">SERVER CONFIGURATOR</div>
        <h1>服务器采购配置</h1>
        <p>选择配置方案，实时计算费用，一键下单。</p>
      </div>
      <div class="head-actions">
        <n-button @click="loadAll" :loading="loading">刷新配置 ↻</n-button>
      </div>
    </div>

    <n-spin :show="loading" description="加载产品配置...">
      <div class="purchase-layout" v-if="!loading">
        <!-- ========== 左侧：配置选择区 ========== -->
        <div class="config-area">
          <!-- 1. 地域/机房选择 -->
          <section class="config-section">
            <div class="section-header">
              <span class="section-step">01</span>
              <h3>选择地域机房</h3>
            </div>
            <n-select
              v-model:value="selectedDatacenter"
              placeholder="请选择机房所在地域"
              :options="datacenterOptions"
              filterable
              style="width: 100%; max-width: 420px;"
            />
            <div v-if="selectedDatacenterObj" class="datacenter-info">
              <span v-if="selectedDatacenterObj.code" class="dc-tag">{{ selectedDatacenterObj.code }}</span>
              <span v-if="selectedDatacenterObj.description" class="dc-desc">{{ selectedDatacenterObj.description }}</span>
            </div>
          </section>

          <n-divider />

          <!-- 2. 服务器型号/规格选择 -->
          <section class="config-section">
            <div class="section-header">
              <span class="section-step">02</span>
              <h3>选择服务器规格</h3>
            </div>
            <div v-if="productCards.length" class="product-grid">
              <div
                v-for="product in productCards"
                :key="String(product.id)"
                class="product-card"
                :class="{ selected: selectedProductId === String(product.id) }"
                @click="selectedProductId = String(product.id)"
              >
                <div class="product-card-head">
                  <span class="product-name">{{ product.name }}</span>
                  <n-tag v-if="product.status === 'active'" type="success" size="small" :bordered="false">可售</n-tag>
                  <n-tag v-else type="warning" size="small" :bordered="false">售罄</n-tag>
                </div>
                <div class="product-specs">
                  <template v-if="parseProductSpec(product).cpu !== '-'">
                    <div class="spec-row">
                      <span class="spec-icon">⚙</span>
                      <span>CPU：{{ parseProductSpec(product).cpu }}</span>
                    </div>
                    <div class="spec-row">
                      <span class="spec-icon">🧠</span>
                      <span>内存：{{ parseProductSpec(product).ram }}</span>
                    </div>
                    <div class="spec-row">
                      <span class="spec-icon">💾</span>
                      <span>存储：{{ parseProductSpec(product).disk }}</span>
                    </div>
                  </template>
                  <template v-else>
                    <div class="spec-row spec-desc">
                      {{ product.description || '标准服务器配置' }}
                    </div>
                  </template>
                </div>
                <div class="product-price">
                  <span class="price-value">{{ formatPrice(parseProductPricing(product).basePrice) }}</span>
                  <span class="price-unit">/月起</span>
                </div>
                <div class="product-card-check" v-if="selectedProductId === String(product.id)">✓</div>
              </div>
            </div>
            <n-empty v-else description="暂无可售产品，请先在产品管理中添加" />
          </section>

          <n-divider />

          <!-- 3. 操作系统选择 -->
          <section class="config-section">
            <div class="section-header">
              <span class="section-step">03</span>
              <h3>选择操作系统</h3>
              <n-tag size="small" :bordered="false" type="info">可选</n-tag>
            </div>
            <div v-if="imageOptions.length" class="image-grid">
              <div
                v-for="img in imageOptions"
                :key="img.value"
                class="image-card"
                :class="{ selected: selectedImageId === img.value }"
                @click="selectedImageId = img.value"
              >
                <span class="image-icon">{{ img.icon }}</span>
                <span class="image-label">{{ img.label }}</span>
              </div>
            </div>
            <n-empty v-else description="暂无可用镜像" size="small" style="padding: 16px 0;" />
          </section>

          <n-divider />

          <!-- 4. 带宽/IP配置 -->
          <section class="config-section">
            <div class="section-header">
              <span class="section-step">04</span>
              <h3>网络配置</h3>
            </div>
            <div class="network-config">
              <div class="network-item">
                <label>公网带宽</label>
                <div class="network-control">
                  <n-input-number
                    v-model:value="bandwidthMbps"
                    :min="0"
                    :max="10000"
                    :step="10"
                    style="width: 180px;"
                  />
                  <span class="network-unit">Mbps</span>
                </div>
                <div class="network-hint">
                  <n-tag size="small" :bordered="false" v-if="bandwidthMbps >= 1000">千兆专线</n-tag>
                  <n-tag size="small" :bordered="false" v-else-if="bandwidthMbps >= 100">百兆以上</n-tag>
                  <n-tag size="small" :bordered="false" v-else>基础带宽</n-tag>
                </div>
              </div>
              <div class="network-item">
                <label>公网 IP 数量</label>
                <div class="network-control">
                  <n-input-number
                    v-model:value="ipCount"
                    :min="0"
                    :max="64"
                    :step="1"
                    style="width: 180px;"
                  />
                  <span class="network-unit">个</span>
                </div>
              </div>
            </div>
          </section>

          <n-divider />

          <!-- 5. 购买时长 -->
          <section class="config-section">
            <div class="section-header">
              <span class="section-step">05</span>
              <h3>购买时长</h3>
            </div>
            <n-radio-group v-model:value="duration" class="duration-group">
              <div
                v-for="opt in durationOptions"
                :key="opt.value"
                class="duration-card"
                :class="{ selected: duration === opt.value }"
                @click="duration = opt.value"
              >
                <n-radio :value="opt.value" class="duration-radio" />
                <div class="duration-label">
                  <span class="duration-name">{{ opt.label }}</span>
                  <n-tag
                    v-if="opt.badge"
                    type="warning"
                    size="small"
                    :bordered="false"
                    class="duration-badge"
                  >
                    {{ opt.badge }}
                  </n-tag>
                </div>
              </div>
            </n-radio-group>
          </section>
        </div>

        <!-- ========== 右侧：价格计算卡片 ========== -->
        <div class="price-sidebar">
          <div class="price-card">
            <div class="price-card-title">配置清单与费用</div>

            <!-- 配置摘要 -->
            <div class="summary-list">
              <div class="summary-row">
                <span class="summary-label">地域机房</span>
                <span class="summary-value">{{ selectedDatacenterObj?.name || '未选择' }}</span>
              </div>
              <div class="summary-row">
                <span class="summary-label">服务器规格</span>
                <span class="summary-value">{{ selectedProduct?.name || '未选择' }}</span>
              </div>
              <div class="summary-row">
                <span class="summary-label">操作系统</span>
                <span class="summary-value">{{ selectedImage?.name || '未选择' }}</span>
              </div>
              <div class="summary-row">
                <span class="summary-label">公网带宽</span>
                <span class="summary-value">{{ bandwidthMbps }} Mbps</span>
              </div>
              <div class="summary-row">
                <span class="summary-label">公网 IP</span>
                <span class="summary-value">{{ ipCount }} 个</span>
              </div>
              <div class="summary-row">
                <span class="summary-label">购买时长</span>
                <span class="summary-value">{{ selectedDuration.label }}</span>
              </div>
            </div>

            <n-divider style="margin: 16px 0;" />

            <!-- 价格明细 -->
            <div class="price-detail">
              <div class="price-line">
                <span>单价（月付）</span>
                <span>{{ formatPrice(unitPrice) }}</span>
              </div>
              <div class="price-line" v-if="discountRate < 1">
                <span>时长折扣</span>
                <span class="discount-text">{{ selectedDuration.badge || '-' }}</span>
              </div>
              <div class="price-line">
                <span>折后月价</span>
                <span>{{ formatPrice(discountedMonthly) }}</span>
              </div>
              <div class="price-line">
                <span>时长</span>
                <span>× {{ selectedDuration.months }} 个月</span>
              </div>
              <div class="price-line savings" v-if="savedAmount > 0">
                <span>节省</span>
                <span>-{{ formatPrice(savedAmount) }}</span>
              </div>
            </div>

            <!-- 优惠券 -->
            <div class="coupon-section">
              <div class="coupon-input-row">
                <input
                  v-model="couponCode"
                  class="coupon-input"
                  placeholder="输入优惠码"
                  @keyup.enter="applyCoupon"
                />
                <n-button size="small" @click="applyCoupon" :disabled="!couponCode.trim()">验证</n-button>
              </div>
              <div v-if="couponApplied" class="coupon-applied">
                <n-tag type="success" size="small" :bordered="false">已优惠 {{ formatPrice(couponDiscount) }}</n-tag>
              </div>
            </div>

            <n-divider style="margin: 16px 0;" />

            <!-- 总价 -->
            <div class="total-section">
              <div class="total-label">应付总额</div>
              <div class="total-value">{{ formatPrice(totalPrice) }}</div>
            </div>

            <!-- 下单按钮 -->
            <n-button
              type="primary"
              block
              strong
              size="large"
              class="buy-btn"
              :loading="submitting"
              :disabled="!selectedProduct || !selectedDatacenter"
              @click="openOrderModal"
            >
              立即购买
            </n-button>

            <p class="buy-hint" v-if="!selectedProduct || !selectedDatacenter">
              请先完成上方配置选择
            </p>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <div v-if="!loading && !products.length" class="empty-state">
        <n-empty description="暂无可售产品">
          <template #extra>
            <span style="color: var(--color-text-tertiary); font-size: 13px;">
              请联系管理员在产品管理中添加可售服务器产品
            </span>
          </template>
        </n-empty>
      </div>
    </n-spin>

    <!-- ========== 订单确认弹窗 ========== -->
    <n-modal
      v-model:show="showOrderModal"
      preset="card"
      title="确认订单"
      style="width: 520px; max-width: 92vw;"
      :mask-closable="!submitting"
      :closable="!submitting"
    >
      <template v-if="!orderSuccess">
        <div class="order-confirm-body">
          <div class="order-summary-grid">
            <div class="order-item">
              <span class="oi-label">产品</span>
              <span class="oi-value">{{ selectedProduct?.name }}</span>
            </div>
            <div class="order-item">
              <span class="oi-label">机房</span>
              <span class="oi-value">{{ selectedDatacenterObj?.name }}</span>
            </div>
            <div class="order-item" v-if="selectedImage">
              <span class="oi-label">操作系统</span>
              <span class="oi-value">{{ selectedImage.name }}</span>
            </div>
            <div class="order-item">
              <span class="oi-label">带宽</span>
              <span class="oi-value">{{ bandwidthMbps }} Mbps</span>
            </div>
            <div class="order-item">
              <span class="oi-label">IP 数量</span>
              <span class="oi-value">{{ ipCount }} 个</span>
            </div>
            <div class="order-item">
              <span class="oi-label">时长</span>
              <span class="oi-value">{{ selectedDuration.label }}</span>
            </div>
          </div>
          <n-divider />
          <div class="order-price-final">
            <span>应付金额</span>
            <strong>{{ formatPrice(totalPrice) }}</strong>
          </div>
          <div class="order-actions">
            <n-button @click="showOrderModal = false" :disabled="submitting">取消</n-button>
            <n-button type="primary" @click="submitOrder" :loading="submitting">确认下单</n-button>
          </div>
        </div>
      </template>
      <template v-else>
        <n-result status="success" title="订单创建成功" :description="`订单编号：${createdOrderId}`">
          <template #footer>
            <n-space justify="center">
              <n-button @click="closeOrderModal">继续采购</n-button>
              <n-button type="primary" @click="closeOrderModal">完成</n-button>
            </n-space>
          </template>
        </n-result>
      </template>
    </n-modal>
  </div>
</template>

<style scoped>
.purchase-page {
  min-height: 100%;
}

/* === 两栏布局 === */
.purchase-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 360px;
  gap: 24px;
  align-items: start;
}

/* === 配置区域 === */
.config-area {
  min-width: 0;
}

.config-section {
  margin-bottom: 4px;
}

.section-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 16px;
}

.section-step {
  display: inline-grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  background: var(--color-primary);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.5px;
  flex-shrink: 0;
}

.section-header h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
}

/* === 机房信息 === */
.datacenter-info {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.dc-tag {
  font-size: 11px;
  color: var(--color-primary);
  background: var(--color-primary-light);
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  font-weight: 600;
  letter-spacing: 0.3px;
}

.dc-desc {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

/* === 产品卡片网格 === */
.product-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 14px;
}

.product-card {
  position: relative;
  background: var(--color-surface);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 16px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.product-card:hover {
  border-color: var(--color-primary-hover);
  box-shadow: 0 4px 12px rgba(22, 93, 255, 0.1);
  transform: translateY(-1px);
}

.product-card.selected {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-light);
  background: var(--color-primary-light);
}

.product-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}

.product-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.product-specs {
  margin-bottom: 12px;
}

.spec-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--color-text-secondary);
  padding: 3px 0;
}

.spec-row.spec-desc {
  color: var(--color-text-tertiary);
  font-style: italic;
}

.spec-icon {
  font-size: 13px;
  flex-shrink: 0;
}

.product-price {
  display: flex;
  align-items: baseline;
  gap: 4px;
  padding-top: 10px;
  border-top: 1px solid var(--color-border-light);
}

.price-value {
  font-size: 18px;
  font-weight: 700;
  color: var(--color-primary);
  font-variant-numeric: tabular-nums;
}

.price-unit {
  font-size: 11px;
  color: var(--color-text-tertiary);
}

.product-card-check {
  position: absolute;
  top: 8px;
  right: 8px;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--color-primary);
  color: #fff;
  display: grid;
  place-items: center;
  font-size: 12px;
  font-weight: 700;
}

/* === 操作系统卡片 === */
.image-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 10px;
}

.image-card {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  background: var(--color-surface);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all 0.15s ease;
  font-size: 13px;
  color: var(--color-text-primary);
}

.image-card:hover {
  border-color: var(--color-primary-hover);
}

.image-card.selected {
  border-color: var(--color-primary);
  background: var(--color-primary-light);
}

.image-icon {
  font-size: 20px;
  flex-shrink: 0;
}

.image-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* === 网络配置 === */
.network-config {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 480px;
}

.network-item {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.network-item label {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text-secondary);
  min-width: 100px;
  flex-shrink: 0;
}

.network-control {
  display: flex;
  align-items: center;
  gap: 8px;
}

.network-unit {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.network-hint {
  margin-left: 4px;
}

/* === 购买时长 === */
.duration-group {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 10px;
}

.duration-card {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  background: var(--color-surface);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all 0.15s ease;
}

.duration-card:hover {
  border-color: var(--color-primary-hover);
}

.duration-card.selected {
  border-color: var(--color-primary);
  background: var(--color-primary-light);
}

.duration-radio {
  pointer-events: none;
}

.duration-label {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.duration-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text-primary);
}

.duration-badge {
  font-size: 10px !important;
  padding: 1px 6px !important;
}

/* === 右侧价格卡片 === */
.price-sidebar {
  position: sticky;
  top: 24px;
}

.price-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  padding: 24px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.06);
}

.price-card-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--color-text-primary);
  margin-bottom: 18px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--color-border-light);
}

/* === 配置摘要 === */
.summary-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.summary-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.summary-label {
  font-size: 12px;
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}

.summary-value {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-text-primary);
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* === 价格明细 === */
.price-detail {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.price-line {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  color: var(--color-text-secondary);
}

.price-line.savings {
  color: var(--color-success);
  font-weight: 500;
}

.discount-text {
  color: var(--color-warning);
  font-weight: 600;
  font-size: 12px;
}

/* === 优惠券 === */
.coupon-section {
  margin-top: 14px;
}

.coupon-input-row {
  display: flex;
  gap: 8px;
  align-items: center;
}

.coupon-input {
  flex: 1;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text-primary);
  font-size: 13px;
  outline: none;
  transition: border-color 0.15s ease;
}

.coupon-input:focus {
  border-color: var(--color-primary);
}

.coupon-input::placeholder {
  color: var(--color-text-disabled);
}

.coupon-applied {
  margin-top: 6px;
}

/* === 总价 === */
.total-section {
  text-align: center;
  padding: 8px 0 18px;
}

.total-label {
  font-size: 12px;
  color: var(--color-text-tertiary);
  margin-bottom: 6px;
}

.total-value {
  font-size: 32px;
  font-weight: 800;
  color: var(--color-primary);
  font-variant-numeric: tabular-nums;
  letter-spacing: -1px;
}

/* === 购买按钮 === */
.buy-btn {
  height: 46px;
  font-size: 15px;
  font-weight: 600;
  border-radius: var(--radius-lg) !important;
  letter-spacing: 0.5px;
}

.buy-hint {
  text-align: center;
  font-size: 12px;
  color: var(--color-text-disabled);
  margin: 10px 0 0;
}

/* === 空状态 === */
.empty-state {
  padding: 60px 20px;
}

/* === 订单确认弹窗 === */
.order-confirm-body {
  padding: 4px 0;
}

.order-summary-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.order-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.oi-label {
  font-size: 11px;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.oi-value {
  font-size: 14px;
  font-weight: 500;
  color: var(--color-text-primary);
}

.order-price-final {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 0;
  font-size: 14px;
  color: var(--color-text-secondary);
}

.order-price-final strong {
  font-size: 24px;
  font-weight: 800;
  color: var(--color-primary);
  font-variant-numeric: tabular-nums;
}

.order-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--color-border-light);
}

/* === 响应式 === */
@media (max-width: 1100px) {
  .purchase-layout {
    grid-template-columns: 1fr;
  }

  .price-sidebar {
    position: static;
  }
}

@media (max-width: 780px) {
  .product-grid {
    grid-template-columns: 1fr;
  }

  .image-grid {
    grid-template-columns: 1fr;
  }

  .duration-group {
    grid-template-columns: repeat(2, 1fr);
  }

  .order-summary-grid {
    grid-template-columns: 1fr;
  }

  .network-item {
    flex-direction: column;
    align-items: flex-start;
  }
}

@media (max-width: 520px) {
  .duration-group {
    grid-template-columns: 1fr;
  }

  .price-card {
    padding: 18px;
  }

  .total-value {
    font-size: 26px;
  }
}
</style>
