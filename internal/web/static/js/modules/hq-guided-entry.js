// Share ownership of plain HQ arrival with the assistant drawer. An explicit
// destination always wins; a remembered drawer is not an explicit request.
export function offerOnEntry(location) {
  const params = new URLSearchParams(location.search);
  return (
    !location.hash &&
    [...params].every(([key, value]) => key === 'mode' && ['details', 'map'].includes(value))
  );
}
export const offerKey = id => `ori.hq.guidedOffered.${id}`;
export function chooserOwnsArrival(dataset, location) {
  if (!dataset?.guidedHq || dataset.hqFirstVisit !== 'pending' || !offerOnEntry(location))
    return false;
  try {
    return dataset.guidedOfferedHere === '1' || !localStorage.getItem(offerKey(dataset.guidedHq));
  } catch (_) {
    return false;
  }
}
