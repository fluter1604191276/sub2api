export default {
  modelIQ: {
    title: 'OpenAI Model IQ Check', description: 'Runs one shared reasoning question per group and shows the latest round.', model: 'Model: {model}', responseModel: 'Response model: {model}', interval: 'Interval: {seconds} seconds', roundStartedAt: 'Round started: {time}', viewQuestion: 'View question', question: 'Question', expectedAnswer: 'Expected answer', modelAnswer: 'Model answer', checkedAt: 'Checked at', multiplier: 'Group multiplier ×{value}', fullOutput: 'Expand full AI output', noResults: 'No check results yet. Results appear after the next scheduled round finishes.', loadError: 'Failed to load model IQ check results', summary: { total: 'Groups checked', correct: 'Correct', incorrect: 'Incorrect', errors: 'Errors', pending: 'Pending' }, status: { correct: 'Correct', incorrect: 'Incorrect', error: 'Error', skipped: 'Skipped', pending: 'Pending' },
  },
}
