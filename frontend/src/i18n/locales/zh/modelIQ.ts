export default {
  modelIQ: {
    title: 'OpenAI 模型智商检测', description: '按模型和分组展示最近一次有效检测结果。', model: '检测模型：{model}', interval: '检测间隔：{seconds} 秒', viewQuestion: '查看本次题目', question: '题目', expectedAnswer: '标准答案', modelAnswer: '模型答案', checkedAt: '检测时间', multiplier: '分组倍率 ×{value}', fullOutput: '展开完整 AI 输出', noResults: '当前没有检测结果。启用后将在下一次计划检测完成后显示。', noResultsForGroup: '暂无有效检测结果。', loadError: '加载模型智商检测结果失败，请稍后重试。', summary: { total: '检测渠道', correct: '正确', incorrect: '错误', noResults: '暂无结果' }, status: { correct: '正确', incorrect: '错误', no_results: '暂无结果' },
  },
}
