/**
 * Form Validation Module
 * Provides real-time validation, character counters, and form state management
 */

const FormValidation = (function () {
  'use strict';

  /**
   * Initialize a character counter for an input/textarea
   * @param {HTMLElement} input - The input element
   * @param {Object} options - Configuration options
   * @param {number} options.maxLength - Maximum character length
   * @param {number} options.warningThreshold - Percentage at which to show warning (default: 80)
   */
  function initCharCounter(input, options = {}) {
    const maxLength = options.maxLength || parseInt(input.getAttribute('maxlength')) || 1000;
    const warningThreshold = options.warningThreshold || 80;

    // Create counter element
    const counter = document.createElement('div');
    counter.className = 'char-counter';
    counter.setAttribute('aria-live', 'polite');

    // Insert after input
    input.parentNode.insertBefore(counter, input.nextSibling);

    // Update function
    function updateCounter() {
      const currentLength = input.value.length;

      const percentage = (currentLength / maxLength) * 100;

      counter.textContent = `${currentLength} / ${maxLength}`;

      // Update styling based on threshold
      counter.classList.remove('char-counter-warning', 'char-counter-danger');
      if (percentage >= 100) {
        counter.classList.add('char-counter-danger');
      } else if (percentage >= warningThreshold) {
        counter.classList.add('char-counter-warning');
      }
    }

    // Listen for input changes
    input.addEventListener('input', updateCounter);

    // Initial update
    updateCounter();

    return { update: updateCounter, element: counter };
  }

  /**
   * Add real-time validation to an input
   * @param {HTMLElement} input - The input element
   * @param {Object} validators - Validation rules
   */
  function initInputValidation(input, validators = {}) {
    const errorElement = document.createElement('div');
    errorElement.className = 'form-error';
    errorElement.style.display = 'none';
    errorElement.setAttribute('role', 'alert');

    // Insert error element after input (or after char counter if present)
    const nextSibling = input.nextElementSibling;
    if (nextSibling && nextSibling.classList.contains('char-counter')) {
      nextSibling.parentNode.insertBefore(errorElement, nextSibling.nextSibling);
    } else {
      input.parentNode.insertBefore(errorElement, input.nextSibling);
    }

    function validate() {
      const value = input.value.trim();
      let isValid = true;
      let errorMessage = '';

      // Required validation
      if (validators.required && !value) {
        isValid = false;
        errorMessage = validators.requiredMessage || 'This field is required';
      }

      // Min length validation
      if (isValid && validators.minLength && value.length < validators.minLength) {
        isValid = false;
        errorMessage =
          validators.minLengthMessage || `Minimum ${validators.minLength} characters required`;
      }

      // Max length validation
      if (isValid && validators.maxLength && value.length > validators.maxLength) {
        isValid = false;
        errorMessage =
          validators.maxLengthMessage || `Maximum ${validators.maxLength} characters allowed`;
      }

      // Pattern validation
      if (isValid && validators.pattern && value) {
        const regex = new RegExp(validators.pattern);
        if (!regex.test(value)) {
          isValid = false;
          errorMessage = validators.patternMessage || 'Invalid format';
        }
      }

      // Custom validation function
      if (isValid && validators.custom && value) {
        const result = validators.custom(value);
        if (result !== true) {
          isValid = false;
          errorMessage = result || 'Invalid value';
        }
      }

      // Update UI
      input.classList.toggle('is-invalid', !isValid);
      input.classList.toggle('is-valid', isValid && value.length > 0);
      errorElement.textContent = errorMessage;
      errorElement.style.display = isValid ? 'none' : 'block';

      return isValid;
    }

    // Debounce validation for better UX
    let timeout;
    function debouncedValidate() {
      clearTimeout(timeout);
      timeout = setTimeout(validate, 300);
    }

    // Listen for input changes
    input.addEventListener('input', debouncedValidate);
    input.addEventListener('blur', validate);

    return { validate, errorElement };
  }

  /**
   * Initialize form validation
   * @param {HTMLFormElement} form - The form element
   * @param {Object} options - Configuration options
   */
  function initForm(form, options = {}) {
    const validators = {};
    const submitBtn =
      options.submitButton || form.querySelector('[type="submit"], .form-submit-btn');

    // Track validation state
    let formIsValid = false;

    function updateSubmitButton() {
      if (submitBtn && options.disableSubmitUntilValid) {
        submitBtn.disabled = !formIsValid;
      }
    }

    function validateAll() {
      formIsValid = true;
      Object.values(validators).forEach(v => {
        if (!v.validate()) {
          formIsValid = false;
        }
      });
      updateSubmitButton();
      return formIsValid;
    }

    // Register input validators
    function registerInput(input, rules) {
      const inputName = input.name || input.id;
      validators[inputName] = initInputValidation(input, rules);

      // Re-validate form on input change
      input.addEventListener('input', () => {
        setTimeout(validateAll, 350);
      });
    }

    // Form submit handler
    form.addEventListener('submit', e => {
      if (!validateAll()) {
        e.preventDefault();
        // Focus first invalid input
        const firstInvalid = form.querySelector('.is-invalid');
        if (firstInvalid) {
          firstInvalid.focus();
        }
      }
    });

    return {
      registerInput,
      validateAll,
      isValid: () => formIsValid
    };
  }

  /**
   * Add a format hint to an input
   * @param {HTMLElement} input - The input element
   * @param {string} hint - The hint text
   */
  function addFormatHint(input, hint) {
    const hintElement = document.createElement('div');
    hintElement.className = 'form-helper';
    hintElement.textContent = hint;

    // Insert after input
    const parent = input.parentNode;
    const nextSibling = input.nextSibling;
    parent.insertBefore(hintElement, nextSibling);

    return hintElement;
  }

  /**
   * Mark a label as required
   * @param {HTMLElement} label - The label element
   */
  function markAsRequired(label) {
    if (!label.classList.contains('required')) {
      label.classList.add('required');
    }
  }

  // Public API
  return {
    initCharCounter,
    initInputValidation,
    initForm,
    addFormatHint,
    markAsRequired
  };
})();

// Export for module systems
if (typeof module !== 'undefined' && module.exports) {
  module.exports = FormValidation;
}
