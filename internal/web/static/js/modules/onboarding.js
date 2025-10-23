// Onboarding module - Handles the first-time user onboarding flow
export class OnboardingManager {
  constructor() {
    this.currentStep = 0;
    this.totalSteps = 3;
    this.modal = null;
    this.modalInstance = null;
  }

  // Initialize the onboarding system
  async init() {
    this.modal = document.getElementById('onboardingModal');
    if (!this.modal) {
      console.error('Onboarding modal not found');
      return;
    }

    // Initialize Bootstrap modal
    this.modalInstance = new bootstrap.Modal(this.modal, {
      backdrop: 'static',
      keyboard: false
    });

    // Setup event listeners
    this.setupEventListeners();

    // Check if onboarding is needed
    const status = await this.checkOnboardingStatus();
    if (status.needs_onboarding) {
      // Show modal after a short delay to let the page load
      setTimeout(() => {
        this.showOnboarding();
      }, 500);
    }
  }

  // Setup event listeners for modal buttons
  setupEventListeners() {
    const nextBtn = document.getElementById('nextStepBtn');
    const prevBtn = document.getElementById('prevStepBtn');
    const skipBtn = document.getElementById('skipOnboardingBtn');
    const completeBtn = document.getElementById('completeOnboardingBtn');
    const closeBtn = document.getElementById('onboardingCloseBtn');

    if (nextBtn) {
      nextBtn.addEventListener('click', () => this.nextStep());
    }

    if (prevBtn) {
      prevBtn.addEventListener('click', () => this.previousStep());
    }

    if (skipBtn) {
      skipBtn.addEventListener('click', () => this.skipOnboarding());
    }

    if (completeBtn) {
      completeBtn.addEventListener('click', () => this.completeOnboarding());
    }

    if (closeBtn) {
      closeBtn.addEventListener('click', () => this.skipOnboarding());
    }

    // Keyboard navigation
    this.keyboardHandler = (event) => {
      // Only handle keyboard events when modal is visible
      if (!this.modal.classList.contains('show')) {
        return;
      }

      switch(event.key) {
        case 'ArrowRight':
        case 'ArrowDown':
          event.preventDefault();
          if (this.currentStep < this.totalSteps - 1) {
            this.nextStep();
          }
          break;
        case 'ArrowLeft':
        case 'ArrowUp':
          event.preventDefault();
          if (this.currentStep > 0) {
            this.previousStep();
          }
          break;
        case 'Escape':
          event.preventDefault();
          this.skipOnboarding();
          break;
        case 'Enter':
          event.preventDefault();
          if (this.currentStep === this.totalSteps - 1) {
            this.completeOnboarding();
          } else {
            this.nextStep();
          }
          break;
      }
    };

    document.addEventListener('keydown', this.keyboardHandler);
  }

  // Check onboarding status from backend
  async checkOnboardingStatus() {
    try {
      const response = await fetch('/api/onboarding/status');
      if (!response.ok) {
        throw new Error('Failed to fetch onboarding status');
      }
      return await response.json();
    } catch (error) {
      console.error('Error checking onboarding status:', error);
      return { needs_onboarding: false };
    }
  }

  // Show the onboarding modal
  showOnboarding() {
    if (this.modalInstance) {
      this.currentStep = 0;
      this.updateStepDisplay();
      this.modalInstance.show();
    }
  }

  // Move to next step
  async nextStep() {
    if (this.currentStep < this.totalSteps - 1) {
      // Mark current step as completed
      await this.completeStep(`step-${this.currentStep}`);

      this.currentStep++;
      this.updateStepDisplay();
    }
  }

  // Move to previous step
  previousStep() {
    if (this.currentStep > 0) {
      this.currentStep--;
      this.updateStepDisplay();
    }
  }

  // Update the step display (show/hide steps, update progress)
  updateStepDisplay() {
    // Hide all steps
    const steps = document.querySelectorAll('.onboarding-step');
    steps.forEach(step => step.classList.add('d-none'));

    // Show current step
    const currentStepElement = document.getElementById(`step-${this.currentStep}`);
    if (currentStepElement) {
      currentStepElement.classList.remove('d-none');
    }

    // Update progress bar
    const progress = ((this.currentStep + 1) / this.totalSteps) * 100;
    const progressBar = document.getElementById('onboardingProgress');
    if (progressBar) {
      progressBar.style.width = `${progress}%`;
      progressBar.setAttribute('aria-valuenow', progress);
    }

    // Update step number
    const stepNum = document.getElementById('currentStepNum');
    if (stepNum) {
      stepNum.textContent = this.currentStep + 1;
    }

    // Update button visibility
    const prevBtn = document.getElementById('prevStepBtn');
    const nextBtn = document.getElementById('nextStepBtn');
    const completeBtn = document.getElementById('completeOnboardingBtn');

    if (prevBtn) {
      if (this.currentStep === 0) {
        prevBtn.classList.add('d-none');
      } else {
        prevBtn.classList.remove('d-none');
      }
    }

    if (nextBtn && completeBtn) {
      if (this.currentStep === this.totalSteps - 1) {
        nextBtn.classList.add('d-none');
        completeBtn.classList.remove('d-none');
      } else {
        nextBtn.classList.remove('d-none');
        completeBtn.classList.add('d-none');
      }
    }
  }

  // Mark a step as completed in the backend
  async completeStep(stepName) {
    try {
      const response = await fetch('/api/onboarding/step', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ step_name: stepName }),
      });

      if (!response.ok) {
        throw new Error('Failed to complete step');
      }

      return await response.json();
    } catch (error) {
      console.error('Error completing step:', error);
    }
  }

  // Skip onboarding
  async skipOnboarding() {
    try {
      const response = await fetch('/api/onboarding/skip', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        throw new Error('Failed to skip onboarding');
      }

      if (this.modalInstance) {
        this.modalInstance.hide();
      }
    } catch (error) {
      console.error('Error skipping onboarding:', error);
    }
  }

  // Complete onboarding
  async completeOnboarding() {
    try {
      // Mark last step as completed
      await this.completeStep(`step-${this.currentStep}`);

      // Mark onboarding as complete
      const response = await fetch('/api/onboarding/complete', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        throw new Error('Failed to complete onboarding');
      }

      if (this.modalInstance) {
        this.modalInstance.hide();
      }
    } catch (error) {
      console.error('Error completing onboarding:', error);
    }
  }

  // Reset onboarding (useful for testing)
  async resetOnboarding() {
    try {
      const response = await fetch('/api/onboarding/reset', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        throw new Error('Failed to reset onboarding');
      }

      return await response.json();
    } catch (error) {
      console.error('Error resetting onboarding:', error);
    }
  }

  // Cleanup method to remove event listeners and prevent memory leaks
  destroy() {
    if (this.keyboardHandler) {
      document.removeEventListener('keydown', this.keyboardHandler);
      this.keyboardHandler = null;
    }
  }
}

// Create a singleton instance
export const onboardingManager = new OnboardingManager();
