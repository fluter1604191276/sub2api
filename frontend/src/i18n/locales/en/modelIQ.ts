export default {
  modelIQ: {
    title: 'OpenAI Model IQ Check', description: 'Shows the most recent valid result for each model and group.', model: 'Model: {model}', interval: 'Interval: {seconds} seconds', viewQuestion: 'View question for this result', question: 'Question', expectedAnswer: 'Expected answer', modelAnswer: 'Model answer', checkedAt: 'Checked at', multiplier: 'Group multiplier ×{value}', fullOutput: 'Expand full AI output', noResults: 'No check results yet. Results appear after the next scheduled round finishes.', noResultsForGroup: 'No valid result yet.', loadError: 'Failed to load model IQ check results. Try again later.', summary: { total: 'Groups checked', correct: 'Correct', incorrect: 'Incorrect', noResults: 'No result' }, status: { correct: 'Correct', incorrect: 'Incorrect', no_results: 'No result' },
  },
}
