// API 封装：统一带 X-User-ID，统一错误处理

const USER_KEY = 'nexus_user_id'

export function currentUser(): string {
  return localStorage.getItem(USER_KEY) || ''
}
export function setCurrentUser(id: string) {
  localStorage.setItem(USER_KEY, id)
}

export interface User {
  id: string
  name: string
  role: string
}

// 3 人小团队用户表（与后端 core.Users 一致）
export const USERS: User[] = [
  { id: 'user1', name: '管理员', role: 'admin' },
  { id: 'user2', name: '经理A', role: 'manager' },
  { id: 'user3', name: '经理B', role: 'manager' },
]

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const uid = currentUser()
  if (!uid) throw new ApiError(401, '请先选择用户')
  const res = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': uid,
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, (data as { error?: string }).error || `请求失败 (${res.status})`)
  }
  return data as T
}

export interface Customer {
  id: string
  name: string
  contact: string
  phone: string
  email: string
  requirements: string
  tech_stack: string
  industry: string
  owner: string
  status: string
  priority: number
  created_at: string
  updated_at: string
}

export interface Supplier {
  id: string
  name: string
  contact: string
  phone: string
  specialties: string[]
  price_level: string
  delivery_speed: number
  quality_rating: number
  created_by: string
}

export interface Solution {
  id: string
  name: string
  description: string
  tech_stack: string[]
  estimated_cost: number
  delivery_days: number
  supplier_id: string
}

export interface Match {
  id: string
  customer_id: string
  solution_id: string
  supplier_id: string
  match_score: number
  match_reason: string
  status: string
  created_by: string
}

export interface MatchCreateReq {
  customer_id: string
  solution_id: string
  budget: number
  desired_days: number
  desired_stack: string[]
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system'
  author: string
  content: string
  at: string
}

export interface Conversation {
  id: string
  owner: string
  subject_type: 'customer' | 'supplier' | 'general'
  subject_id: string
  subject_name: string
  title: string
  skill: string
  claude_session_id: string
  summary: string
  summary_at: string
  messages: ChatMessage[]
  created_at: string
  updated_at: string
}

export interface Skill {
  name: string
  content: string
}

export const api = {
  listCustomers: () => call<Customer[]>('GET', '/api/v1/customers'),
  createCustomer: (c: Partial<Customer>) => call<Customer>('POST', '/api/v1/customers', c),
  updateCustomer: (id: string, c: Partial<Customer>) => call<Customer>('PUT', `/api/v1/customers/${id}`, c),
  deleteCustomer: (id: string) => call('DELETE', `/api/v1/customers/${id}`),

  listSuppliers: () => call<Supplier[]>('GET', '/api/v1/suppliers'),
  createSupplier: (s: Partial<Supplier>) => call<Supplier>('POST', '/api/v1/suppliers', s),

  listSolutions: () => call<Solution[]>('GET', '/api/v1/solutions'),
  createSolution: (s: Partial<Solution>) => call<Solution>('POST', '/api/v1/solutions', s),

  listMatches: () => call<Match[]>('GET', '/api/v1/matches'),
  createMatch: (m: MatchCreateReq) =>
    call<{ match: Match; breakdown: Record<string, number> }>('POST', '/api/v1/matches', m),
  updateMatchStatus: (id: string, status: string) =>
    call<Match>('PUT', `/api/v1/matches/${id}`, { status }),

  dashboard: () =>
    call<{ customers: number; suppliers: number; solutions: number; matches: number; deals: number }>(
      'GET',
      '/api/v1/stats/dashboard'
    ),

  // AI 聊天
  listConversations: (all = false) =>
    call<Conversation[]>('GET', `/api/v1/conversations${all ? '?all=1' : ''}`),
  getConversation: (id: string) => call<Conversation>('GET', `/api/v1/conversations/${id}`),
  createConversation: (c: { subject_type: string; subject_id?: string; title?: string; skill?: string }) =>
    call<Conversation>('POST', '/api/v1/conversations', c),
  sendMessage: (id: string, content: string) =>
    call<{ user_message: ChatMessage; ai_message: ChatMessage }>(
      'POST', `/api/v1/conversations/${id}/chat`, { content }
    ),
  refreshSummary: (id: string) =>
    call<{ summary: string }>('POST', `/api/v1/conversations/${id}/summary`),

  // AI 技能
  listSkills: () => call<Skill[]>('GET', '/api/v1/skills'),
  putSkill: (name: string, content: string) =>
    call('PUT', `/api/v1/admin/skills/${encodeURIComponent(name)}`, { content }),
}
