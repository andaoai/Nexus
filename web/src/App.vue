<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  api, ApiError, USERS, currentUser, setCurrentUser,
  type Customer, type Supplier, type Solution, type Match,
} from './api'

const tab = ref<'dashboard' | 'customers' | 'suppliers' | 'solutions' | 'matches'>('dashboard')
const uid = ref(currentUser())
const error = ref('')
const isAdmin = computed(() => USERS.find(u => u.id === uid.value)?.role === 'admin')

const customers = ref<Customer[]>([])
const suppliers = ref<Supplier[]>([])
const solutions = ref<Solution[]>([])
const matches = ref<Match[]>([])
const stats = ref({ customers: 0, suppliers: 0, solutions: 0, matches: 0, deals: 0 })

async function run(fn: () => Promise<void>) {
  error.value = ''
  try { await fn() } catch (e) {
    error.value = e instanceof ApiError ? e.message : String(e)
  }
}

function switchUser(id: string) {
  uid.value = id
  setCurrentUser(id)
  refresh()
}

async function refresh() {
  if (!uid.value) return
  await run(async () => {
    const [cs, ss, sols, ms, st] = await Promise.all([
      api.listCustomers(), api.listSuppliers(), api.listSolutions(), api.listMatches(), api.dashboard(),
    ])
    customers.value = cs || []
    suppliers.value = ss || []
    solutions.value = sols || []
    matches.value = ms || []
    stats.value = st
  })
}

onMounted(refresh)

// ---- 客户 ----
const custModal = ref<HTMLDialogElement>()
const custForm = ref<Partial<Customer>>({})
function newCustomer() {
  custForm.value = { name: '', contact: '', phone: '', email: '', industry: '', requirements: '', tech_stack: '', priority: 3 }
  custModal.value?.showModal()
}
function saveCustomer() {
  run(async () => {
    if (custForm.value.id) await api.updateCustomer(custForm.value.id, custForm.value)
    else await api.createCustomer(custForm.value)
    custModal.value?.close()
    await refresh()
  })
}
function delCustomer(c: Customer) {
  if (!confirm(`确认删除客户「${c.name}」？`)) return
  run(async () => { await api.deleteCustomer(c.id); await refresh() })
}

// ---- 供应商 ----
const supModal = ref<HTMLDialogElement>()
const supForm = ref<Partial<Supplier> & { specialties_text?: string }>({})
function newSupplier() {
  supForm.value = { name: '', contact: '', phone: '', specialties: [], price_level: '中端', delivery_speed: 4, quality_rating: 4, specialties_text: '' }
  supModal.value?.showModal()
}
function saveSupplier() {
  run(async () => {
    await api.createSupplier({
      ...supForm.value,
      specialties: (supForm.value.specialties_text || '').split(/[,，\s]+/).filter(Boolean),
    })
    supModal.value?.close()
    await refresh()
  })
}

// ---- 方案 ----
const solModal = ref<HTMLDialogElement>()
const solForm = ref<Partial<Solution> & { tech_stack_text?: string }>({})
function newSolution() {
  solForm.value = { name: '', description: '', estimated_cost: 0, delivery_days: 30, supplier_id: '', tech_stack_text: '' }
  solModal.value?.showModal()
}
function saveSolution() {
  run(async () => {
    await api.createSolution({
      ...solForm.value,
      tech_stack: (solForm.value.tech_stack_text || '').split(/[,，\s]+/).filter(Boolean),
    })
    solModal.value?.close()
    await refresh()
  })
}

// ---- 匹配 ----
const matchModal = ref<HTMLDialogElement>()
const matchForm = ref<{ customer_id: string; solution_id: string; budget: number; desired_days: number; desired_stack_text: string }>(
  { customer_id: '', solution_id: '', budget: 0, desired_days: 30, desired_stack_text: '' }
)
const lastBreakdown = ref<Record<string, number> | null>(null)
function newMatch() {
  lastBreakdown.value = null
  matchModal.value?.showModal()
}
function saveMatch() {
  run(async () => {
    const res = await api.createMatch({
      customer_id: matchForm.value.customer_id,
      solution_id: matchForm.value.solution_id,
      budget: Number(matchForm.value.budget) || 0,
      desired_days: Number(matchForm.value.desired_days) || 0,
      desired_stack: (matchForm.value.desired_stack_text || '').split(/[,，\s]+/).filter(Boolean),
    })
    lastBreakdown.value = res.breakdown
    await refresh()
  })
}
function setMatchStatus(m: Match, status: string) {
  run(async () => { await api.updateMatchStatus(m.id, status); await refresh() })
}

function ownerName(id: string) { return USERS.find(u => u.id === id)?.name || id }
function solutionName(id: string) { return solutions.value.find(s => s.id === id)?.name || id }
function customerName(id: string) { return customers.value.find(c => c.id === id)?.name || id }

function scoreTag(score: number) {
  if (score >= 80) return 'green'
  if (score >= 60) return 'yellow'
  return 'red'
}
function statusTag(s: string) {
  return { 已签约: 'green', 已确认: 'yellow', 待确认: 'gray', 已放弃: 'red' }[s] || 'gray'
}
const matchStatuses = ['待确认', '已确认', '已签约', '已放弃']
</script>

<template>
  <header class="topbar">
    <span class="brand">NexusCRM</span>
    <nav>
      <button :class="{ active: tab === 'dashboard' }" @click="tab = 'dashboard'">仪表盘</button>
      <button :class="{ active: tab === 'customers' }" @click="tab = 'customers'">客户</button>
      <button :class="{ active: tab === 'suppliers' }" @click="tab = 'suppliers'">供应商</button>
      <button :class="{ active: tab === 'solutions' }" @click="tab = 'solutions'">方案</button>
      <button :class="{ active: tab === 'matches' }" @click="tab = 'matches'">匹配</button>
    </nav>
    <select :value="uid" @change="switchUser(($event.target as HTMLSelectElement).value)">
      <option value="" disabled>选择用户…</option>
      <option v-for="u in USERS" :key="u.id" :value="u.id">{{ u.name }}（{{ u.id }}）</option>
    </select>
  </header>

  <main class="container">
    <p v-if="error" class="error-msg">{{ error }}</p>
    <p v-if="!uid" class="muted">请先在右上角选择登录用户（演示：user1=管理员，user2/user3=客户经理）。</p>

    <!-- 仪表盘 -->
    <section v-if="tab === 'dashboard'">
      <div class="stats">
        <div class="stat"><b>{{ stats.customers }}</b><span>客户</span></div>
        <div class="stat"><b>{{ stats.suppliers }}</b><span>供应商</span></div>
        <div class="stat"><b>{{ stats.solutions }}</b><span>方案</span></div>
        <div class="stat"><b>{{ stats.matches }}</b><span>匹配</span></div>
        <div class="stat"><b>{{ stats.deals }}</b><span>已签约</span></div>
      </div>
      <div class="card">
        <h3>最近匹配</h3>
        <table>
          <tr><th>客户</th><th>方案</th><th>匹配度</th><th>状态</th><th>创建人</th></tr>
          <tr v-for="m in matches.slice(0, 5)" :key="m.id">
            <td>{{ customerName(m.customer_id) }}</td>
            <td>{{ solutionName(m.solution_id) }}</td>
            <td><span class="tag" :class="scoreTag(m.match_score)">{{ m.match_score }}</span></td>
            <td><span class="tag" :class="statusTag(m.status)">{{ m.status }}</span></td>
            <td>{{ ownerName(m.created_by) }}</td>
          </tr>
          <tr v-if="!matches.length"><td colspan="5" class="muted">暂无匹配记录</td></tr>
        </table>
      </div>
    </section>

    <!-- 客户 -->
    <section v-if="tab === 'customers'" class="card">
      <button class="btn primary" @click="newCustomer" :disabled="!uid">+ 新建客户</button>
      <table style="margin-top:12px">
        <tr><th>名称</th><th>联系人</th><th>行业</th><th>需求</th><th>状态</th><th>优先级</th><th>负责人</th><th>操作</th></tr>
        <tr v-for="c in customers" :key="c.id">
          <td>{{ c.name }}</td>
          <td>{{ c.contact }} {{ c.phone }}</td>
          <td>{{ c.industry }}</td>
          <td class="muted">{{ c.requirements.slice(0, 30) }}</td>
          <td><span class="tag gray">{{ c.status }}</span></td>
          <td>{{ c.priority }}</td>
          <td>{{ ownerName(c.owner) }}</td>
          <td>
            <button class="btn" @click="custForm = { ...c }; custModal?.showModal()">编辑</button>
            <button class="btn danger" @click="delCustomer(c)">删除</button>
          </td>
        </tr>
        <tr v-if="!customers.length"><td colspan="8" class="muted">暂无客户</td></tr>
      </table>
    </section>

    <!-- 供应商 -->
    <section v-if="tab === 'suppliers'" class="card">
      <button v-if="isAdmin" class="btn primary" @click="newSupplier">+ 新增供应商</button>
      <p v-else class="muted">供应商由管理员维护（只读）。</p>
      <table style="margin-top:12px">
        <tr><th>名称</th><th>联系人</th><th>专长</th><th>价位</th><th>交付速度</th><th>质量评分</th></tr>
        <tr v-for="s in suppliers" :key="s.id">
          <td>{{ s.name }}</td>
          <td>{{ s.contact }} {{ s.phone }}</td>
          <td class="muted">{{ s.specialties?.join(' / ') }}</td>
          <td>{{ s.price_level }}</td>
          <td>{{ s.delivery_speed }}/5</td>
          <td>{{ s.quality_rating }}</td>
        </tr>
        <tr v-if="!suppliers.length"><td colspan="6" class="muted">暂无供应商</td></tr>
      </table>
    </section>

    <!-- 方案 -->
    <section v-if="tab === 'solutions'" class="card">
      <button v-if="isAdmin" class="btn primary" @click="newSolution">+ 新增方案</button>
      <p v-else class="muted">方案由管理员维护（只读）。</p>
      <table style="margin-top:12px">
        <tr><th>名称</th><th>供应商</th><th>技术栈</th><th>报价（元）</th><th>交付（天）</th></tr>
        <tr v-for="s in solutions" :key="s.id">
          <td>{{ s.name }}</td>
          <td>{{ suppliers.find(x => x.id === s.supplier_id)?.name || s.supplier_id }}</td>
          <td class="muted">{{ s.tech_stack?.join(' / ') }}</td>
          <td>{{ s.estimated_cost.toLocaleString() }}</td>
          <td>{{ s.delivery_days }}</td>
        </tr>
        <tr v-if="!solutions.length"><td colspan="5" class="muted">暂无方案</td></tr>
      </table>
    </section>

    <!-- 匹配 -->
    <section v-if="tab === 'matches'" class="card">
      <button class="btn primary" @click="newMatch" :disabled="!uid">+ 新建匹配</button>
      <table style="margin-top:12px">
        <tr><th>客户</th><th>方案</th><th>匹配度</th><th>灯</th><th>状态</th><th>创建人</th><th>操作</th></tr>
        <tr v-for="m in matches" :key="m.id">
          <td>{{ customerName(m.customer_id) }}</td>
          <td>{{ solutionName(m.solution_id) }}</td>
          <td><b>{{ m.match_score }}</b></td>
          <td>
            <span class="tag" :class="m.match_reason?.startsWith('绿') ? 'green' : m.match_reason?.startsWith('黄') ? 'yellow' : 'red'">
              {{ m.match_reason?.startsWith('绿') ? '🟢' : m.match_reason?.startsWith('黄') ? '🟡' : '🔴' }}
            </span>
          </td>
          <td>
            <select :value="m.status" @change="setMatchStatus(m, ($event.target as HTMLSelectElement).value)"
              :disabled="m.created_by !== uid && !isAdmin">
              <option v-for="s in matchStatuses" :key="s">{{ s }}</option>
            </select>
          </td>
          <td>{{ ownerName(m.created_by) }}</td>
        </tr>
        <tr v-if="!matches.length"><td colspan="7" class="muted">暂无匹配</td></tr>
      </table>
    </section>
  </main>

  <!-- 客户弹窗 -->
  <dialog ref="custModal" class="modal">
    <h3>{{ custForm.id ? '编辑客户' : '新建客户' }}</h3>
    <div class="form-grid">
      <div><label>客户名称 *</label><input v-model="custForm.name" /></div>
      <div><label>行业</label><input v-model="custForm.industry" /></div>
      <div><label>联系人</label><input v-model="custForm.contact" /></div>
      <div><label>电话</label><input v-model="custForm.phone" /></div>
      <div><label>邮箱</label><input v-model="custForm.email" /></div>
      <div><label>状态</label>
        <select v-model="custForm.status">
          <option>跟进中</option><option>已报价</option><option>已成交</option><option>已流失</option>
        </select>
      </div>
      <div class="full"><label>需求描述</label><textarea v-model="custForm.requirements" rows="2"></textarea></div>
      <div class="full"><label>现有技术栈 / 兼容要求</label><input v-model="custForm.tech_stack" /></div>
      <div><label>优先级 1-5</label><input type="number" min="1" max="5" v-model.number="custForm.priority" /></div>
    </div>
    <div class="actions">
      <button class="btn" @click="custModal?.close()">取消</button>
      <button class="btn primary" @click="saveCustomer">保存</button>
    </div>
  </dialog>

  <!-- 供应商弹窗 -->
  <dialog ref="supModal" class="modal">
    <h3>新增供应商</h3>
    <div class="form-grid">
      <div><label>名称 *</label><input v-model="supForm.name" /></div>
      <div><label>联系人</label><input v-model="supForm.contact" /></div>
      <div><label>电话</label><input v-model="supForm.phone" /></div>
      <div><label>价位</label>
        <select v-model="supForm.price_level">
          <option>低端</option><option>中端</option><option>中高端</option><option>高端</option>
        </select>
      </div>
      <div><label>专长（逗号分隔）</label><input v-model="supForm.specialties_text" /></div>
      <div><label>交付速度 1-5</label><input type="number" min="1" max="5" v-model.number="supForm.delivery_speed" /></div>
    </div>
    <div class="actions">
      <button class="btn" @click="supModal?.close()">取消</button>
      <button class="btn primary" @click="saveSupplier">保存</button>
    </div>
  </dialog>

  <!-- 方案弹窗 -->
  <dialog ref="solModal" class="modal">
    <h3>新增方案</h3>
    <div class="form-grid">
      <div><label>方案名称 *</label><input v-model="solForm.name" /></div>
      <div><label>供应商</label>
        <select v-model="solForm.supplier_id">
          <option value=""></option>
          <option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.name }}</option>
        </select>
      </div>
      <div><label>报价（元）</label><input type="number" v-model.number="solForm.estimated_cost" /></div>
      <div><label>交付周期（天）</label><input type="number" v-model.number="solForm.delivery_days" /></div>
      <div class="full"><label>技术栈（逗号分隔）</label><input v-model="solForm.tech_stack_text" /></div>
      <div class="full"><label>描述</label><textarea v-model="solForm.description" rows="2"></textarea></div>
    </div>
    <div class="actions">
      <button class="btn" @click="solModal?.close()">取消</button>
      <button class="btn primary" @click="saveSolution">保存</button>
    </div>
  </dialog>

  <!-- 匹配弹窗 -->
  <dialog ref="matchModal" class="modal">
    <h3>新建匹配（需求 ↔ 方案）</h3>
    <div class="form-grid">
      <div><label>客户 *</label>
        <select v-model="matchForm.customer_id">
          <option value="" disabled></option>
          <option v-for="c in customers" :key="c.id" :value="c.id">{{ c.name }}</option>
        </select>
      </div>
      <div><label>方案 *</label>
        <select v-model="matchForm.solution_id">
          <option value="" disabled></option>
          <option v-for="s in solutions" :key="s.id" :value="s.id">{{ s.name }}（报价 {{ s.estimated_cost }}）</option>
        </select>
      </div>
      <div><label>客户预算（元）</label><input type="number" v-model.number="matchForm.budget" /></div>
      <div><label>期望交付（天）</label><input type="number" v-model.number="matchForm.desired_days" /></div>
      <div class="full"><label>期望技术栈（逗号分隔，可选）</label><input v-model="matchForm.desired_stack_text" /></div>
    </div>
    <p v-if="lastBreakdown" class="muted" style="margin-top:10px">
      已创建。得分构成：预算 {{ (lastBreakdown.budget * 100).toFixed(0) }} · 技术 {{ (lastBreakdown.tech * 100).toFixed(0) }} · 时间 {{ (lastBreakdown.time * 100).toFixed(0) }}
    </p>
    <div class="actions">
      <button class="btn" @click="matchModal?.close()">关闭</button>
      <button class="btn primary" @click="saveMatch" :disabled="!matchForm.customer_id || !matchForm.solution_id">计算匹配度并创建</button>
    </div>
  </dialog>
</template>
