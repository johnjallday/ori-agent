// Canonical Tickets owns these two existing parameters. Keep them separate
// from the legacy task/run URL vocabulary and retain unrelated owners' keys.
export function readTicketURLState(search = '') {
  const params = new URLSearchParams(search);
  return { ticket: params.get('ticket') || '', tickets: params.get('tickets') || '' };
}

export function writeTicketURLState(search = '', state = {}) {
  const params = new URLSearchParams(search);
  params.delete('ticket');
  params.delete('tickets');
  if (state.ticket) params.set('ticket', state.ticket);
  if (state.tickets && (state.tickets !== 'all' || !state.ticket))
    params.set('tickets', state.tickets);
  return params.toString();
}

export function ticketURLStatesEqual(a, b) {
  return writeTicketURLState('', a || {}) === writeTicketURLState('', b || {});
}
