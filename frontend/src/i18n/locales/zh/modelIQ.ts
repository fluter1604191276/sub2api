export default {
  modelIQ: {
    title: 'OpenAI 模型智商检测', description: '按分组统一执行推理题并展示最新一轮结果。', model: '检测模型：{model}', responseModel: '返回模型：{model}', interval: '检测间隔：{seconds} 秒', roundStartedAt: '本轮开始：{time}', viewQuestion: '查看题目', question: '题目', expectedAnswer: '标准答案', modelAnswer: '模型答案', checkedAt: '检测时间', multiplier: '分组倍率 ×{value}', fullOutput: '展开完整 AI 输出', noResults: '当前没有检测结果。启用后将在下一次计划检测完成后显示。', loadError: '加载模型智商检测结果失败', summary: { total: '检测渠道', correct: '正确', incorrect: '错误', errors: '异常', pending: '等待中' }, status: { correct: '正确', incorrect: '错误', error: '异常', skipped: '已跳过', pending: '等待中' },
  },
}
