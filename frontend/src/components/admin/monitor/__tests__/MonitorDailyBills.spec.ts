import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import MonitorDailyBills from '../MonitorDailyBills.vue'
import { getDailyBills } from '@/api/admin/channelMonitor'

vi.mock('@/api/admin/channelMonitor', () => ({ getDailyBills: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('daily probe bills', () => {
  it('shows known estimates rather than budget reservations', async () => {
    vi.mocked(getDailyBills).mockResolvedValue({ timezone: 'Asia/Shanghai', as_of: '2026-09-10T20:00:00+08:00', period_start: '2026-08-12T00:00:00+08:00', days: [
      { date: '2026-09-10', base_cost_usd: 0.25, account_cost_usd: 0.025, costed_checks: 1, checks: 3, unknown_cost_checks: 1, failed_checks: 1, historical_partial: false },
    ] })
    const wrapper = mount(MonitorDailyBills, { props: { budget: { today_estimated_cost_usd: 12, daily_budget_usd: 20, exhausted: false, resets_at: '' } }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Icon: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('$0.250000')
    expect(wrapper.text()).not.toContain('$12')
    expect(wrapper.text()).toContain('$0.025000')
    wrapper.unmount()
  })
  it('does not report zero cost on load failure', async () => {
    vi.mocked(getDailyBills).mockRejectedValue(new Error('offline'))
    const wrapper = mount(MonitorDailyBills, { props: { budget: { today_estimated_cost_usd: 12, daily_budget_usd: 20, exhausted: false, resets_at: '' } }, global: { stubs: { BaseDialog: true, Icon: true } } })
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('$0.000000')
    wrapper.unmount()
  })
})
