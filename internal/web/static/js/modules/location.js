// Location Indicator Module
// Displays current location and polls for changes

let locationCheckInterval = null;
let currentLocation = null;

// Initialize location indicator on page load
async function initLocationIndicator() {
  // Check immediately on load
  await updateLocationIndicator();

  // Poll every 30 seconds for location changes
  locationCheckInterval = setInterval(updateLocationIndicator, 30 * 1000);
}

// Fetch current location from API and update UI
async function updateLocationIndicator() {
  try {
    const response = await fetch('/api/location/current');
    if (!response.ok) {
      console.error('Failed to fetch current location:', response.status);
      showLocationIndicator('Unknown', 'warning');
      return;
    }

    const data = await response.json();
    const location = data.location || 'Unknown';

    // Only update UI if location changed
    if (location !== currentLocation) {
      currentLocation = location;
      showLocationIndicator(location, location === 'Unknown' ? 'warning' : 'success');
    }
  } catch (error) {
    console.error('Error fetching current location:', error);
    showLocationIndicator('Unknown', 'danger');
  }
}

// Display location indicator in navbar
function showLocationIndicator(location, status = 'success') {
  const indicator = document.getElementById('locationIndicator');
  if (!indicator) {
    return;
  }

  // Status color mapping
  const statusColors = {
    success: 'var(--success-color, #10b981)',
    warning: 'var(--warning-color, #f59e0b)',
    danger: 'var(--danger-color, #ef4444)'
  };

  const color = statusColors[status] || statusColors.success;

  // Location icon SVG
  const icon = `
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" class="me-2" style="color: ${color};">
      <path d="M12 2C8.13 2 5 5.13 5 9c0 5.25 7 13 7 13s7-7.75 7-13c0-3.87-3.13-7-7-7zm0 9.5c-1.38 0-2.5-1.12-2.5-2.5s1.12-2.5 2.5-2.5 2.5 1.12 2.5 2.5-1.12 2.5-2.5 2.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>
  `;

  indicator.innerHTML = `
    ${icon}
    <span class="fw-medium" style="color: var(--text-secondary); font-size: 0.875rem;">${location}</span>
  `;

  // Add tooltip
  indicator.setAttribute('title', `Current location: ${location}`);
}

// Clear location check interval (useful for cleanup)
function stopLocationIndicator() {
  if (locationCheckInterval) {
    clearInterval(locationCheckInterval);
    locationCheckInterval = null;
  }
}

// Manual location refresh (can be called externally)
async function refreshLocation() {
  await updateLocationIndicator();
}

// Make functions globally available
window.initLocationIndicator = initLocationIndicator;
window.updateLocationIndicator = updateLocationIndicator;
window.refreshLocation = refreshLocation;
window.stopLocationIndicator = stopLocationIndicator;

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initLocationIndicator);
} else {
  initLocationIndicator();
}
