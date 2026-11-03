/**
 * English UI strings. Every candidate-facing string lives here so that another language
 * (e.g. `tr.ts` exporting `const tr: Messages`) can be added without touching components.
 * Parameterised strings are functions so translations can reorder words and handle plurals.
 */

const plural = (n: number, one: string, other: string) => (n === 1 ? one : other);

export const en = {
  meta: {
    appName: 'Technical interview',
    titleTemplate: '%s · Technical interview',
    invitationTitle: 'Your invitation',
    interviewTitle: 'Interview',
  },

  layout: {
    skipToContent: 'Skip to main content',
    noscript: 'This interview needs JavaScript. Turn it on in your browser settings, then reload this page.',
    brand: 'Technical interview',
    footer:
      'This interview is structured and reviewed by people. No hiring decision is made automatically.',
  },

  banner: {
    preProduction: 'Pre-production build — not for use with real candidates',
  },

  home: {
    heading: 'Technical interview',
    body: 'To begin, open the invitation link you received. The link opens this site and asks you to confirm before anything happens.',
  },

  notFound: {
    heading: 'Page not found',
    body: 'Check the address, or open the invitation link you received.',
    home: 'Go to the start page',
  },

  errorBoundary: {
    heading: 'Something went wrong',
    body: 'An unexpected problem stopped this page. Anything you already submitted is saved.',
    retry: 'Try again',
  },

  common: {
    loading: 'Loading…',
    retry: 'Try again',
    reference: (id: string) => `Reference: ${id}`,
    required: 'Required',
    optional: 'Optional',
  },

  invitation: {
    heading: 'Welcome to your technical interview',
    checking: 'Checking your invitation link…',
    intro: 'This link is personal to you. When you continue, this browser is signed in to your interview.',
    whatNext:
      'Next, you will read how your information is used and make your choices. Then you can check your microphone, or choose to type your answers.',
    securityNote: 'For your security, the access code has been removed from the address bar.',
    continue: 'Continue',
    continuing: 'Continuing…',
    missing: 'This page needs the full invitation link. Open the link from your invitation again.',
    malformed: 'This invitation link looks incomplete. Copy the whole link from your invitation and open it again.',
    queryToken:
      'This link is not in the expected format. Use the original invitation link exactly as you received it.',
    consumed:
      'This invitation was already used in this window. If your interview did not open, open the original invitation link again.',
  },

  interview: {
    loading: 'Loading your interview…',
    sessionLostHeading: 'We could not open your interview',
    sessionLostBody:
      'Your session may have ended, or the interview was opened in another browser or on another device. Open your invitation link again to continue.',
    connectionProblem: 'We are having trouble reaching the interview service. We will keep trying.',
    waitingHeading: 'One moment',
    waitingBody: 'Your interview is being updated…',
    titles: {
      consent: 'Your choices',
      setup: 'Check your setup',
      room: 'Interview in progress',
      completed: 'Interview complete',
      cancelled: 'Interview ended',
      waiting: 'Interview',
    },
    announcements: {
      consent: 'Please read the notice and make a choice for each item.',
      setup: 'Your choices are saved. Next, check your setup.',
      calibration: 'The interview has started. The interviewer will check your audio first.',
      question: (n: number, total: number) => `Question ${n} of ${total}.`,
      followUp: 'Follow-up question.',
      paused: 'The interview is paused. Your microphone is off.',
      completed: 'The interview is complete. Thank you.',
      cancelled: 'The interview has ended.',
      answerSent: 'Your answer was sent.',
      finishing: 'Finishing your answer…',
    },
  },

  consent: {
    draftBanner: 'Draft notice. This text has not been approved for use with real candidates.',
    loadingNotice: 'Loading the notice…',
    noticeError: 'We could not load the notice.',
    sectionsLabel: 'About this interview',
    choicesHeading: 'Your choices',
    choicesIntro: 'Choose Yes or No for each item. Nothing is selected for you.',
    requiredExplanation:
      'Items marked Required are needed for the interview. If you decline any of them, the interview will be cancelled.',
    voiceExplanation:
      'If you decline the live voice conversation, you can still take the interview by typing your answers.',
    yes: 'Yes, I agree',
    no: 'No, I do not agree',
    dataInvolved: 'Information involved:',
    keptFor: 'Kept for:',
    days: (n: number) => `${n} ${plural(n, 'day', 'days')}`,
    notKept: 'Not kept. Recording is not set up for this interview.',
    legalBasis: 'Legal basis:',
    basis: {
      consent: 'Your consent',
      contract: 'Needed for the interview process',
      legitimate_interest: 'Legitimate interest',
      legal_obligation: 'Legal obligation',
      to_be_determined_by_legal: 'Not yet determined (draft notice)',
    } as Record<string, string>,
    unknownBasis: 'Not stated',
    dataCategory: {
      live_audio_stream: 'Live microphone audio',
      transcript: 'Written transcript of your answers',
      interaction_timing: 'Timing of the conversation',
      audio_recording: 'Recording of your microphone audio',
      assessment_output: 'Assessment notes for human reviewers',
    } as Record<string, string>,
    unknownCategory: 'Other information',
    declineRequiredWarning: 'Declining this will cancel the interview.',
    declineVoiceNote: 'You will type your answers instead of speaking.',
    submit: 'Save my choices',
    submitting: 'Saving…',
    errorSummaryHeading: 'Make a choice for every item',
    errorSummaryItem: (title: string) => `Choose Yes or No for “${title}”`,
    missingChoice: 'Choose Yes or No.',
    confirmDeclineTitle: 'Cancel the interview?',
    confirmDeclineBody: (titles: readonly string[]) =>
      `You declined: ${titles.join('; ')}. The interview cannot go ahead without ${plural(titles.length, 'it', 'them')} and will be cancelled. This cannot be undone.`,
    confirmDeclineConfirm: 'Decline and cancel the interview',
    confirmDeclineCancel: 'Go back to my choices',
  },

  setup: {
    heading: 'Check your setup',
    intro: 'Choose how you would like to answer. The interviewer speaks in both modes and captions are shown on screen.',
    speakersHint: 'Turn on your speakers or headphones. Headphones help avoid echo.',
    voiceHeading: 'Answer by voice',
    voiceIntro: 'Test your microphone first. Speak normally and watch the level meter move.',
    checkMic: 'Test my microphone',
    checkMicAgain: 'Test again',
    requesting: 'Waiting for microphone permission…',
    micReady: 'Your microphone is on.',
    hearingYou: 'We can hear you.',
    speakNow: 'Say a few words to see the level move.',
    levelLabel: 'Microphone input level',
    startVoice: 'Start the interview with voice',
    textHeading: 'Answer by typing',
    textIntro: 'You can type your answers instead. Your microphone is not used.',
    startText: 'Use text responses instead',
    starting: 'Starting…',
    textRecommended: 'You can continue by typing your answers.',
    voiceDeclined: 'You chose not to use the live voice conversation, so this interview uses typed answers.',
    voiceRefused: 'Voice mode is not available for this interview. You can continue by typing your answers.',
    micErrors: {
      permission_denied:
        'Microphone access is blocked. To use your voice, allow microphone access for this site in your browser settings, then test again.',
      no_device: 'No microphone was found. Connect a microphone and test again.',
      device_in_use:
        'Your microphone could not be started. Another application may be using it. Close other apps that use the microphone and test again.',
      insecure_context: 'This browser cannot use the microphone on this page.',
      device_lost: 'Your microphone was disconnected. Reconnect it and test again.',
      unknown: 'The microphone test did not work. Test again.',
    },
    withdraw: 'Withdraw from the interview',
  },

  room: {
    heading: 'Interview',
    progress: (done: number, total: number) => `${done} of ${total} ${plural(total, 'question', 'questions')} answered`,
    statusLabel: 'Status:',
    status: {
      connecting: 'Connecting…',
      reconnecting: 'Reconnecting…',
      disconnected: 'Disconnected',
      paused: 'Paused',
      interviewer_speaking: 'Interviewer speaking',
      listening: 'Listening',
    },
    listeningText: 'Waiting for your typed answer',
    recording: 'Your microphone audio is being recorded',
    micOn: 'Microphone on',
    micOff: 'Microphone off',
    micStarting: 'Starting microphone…',
    calibrationHeading: 'Audio check',
    calibrationBody:
      'The interviewer will greet you and check that you can hear each other. Nothing you say now is assessed. Select Begin when you are ready for the first question.',
    calibrationBodyText:
      'The interviewer will greet you. Nothing you write now is assessed. Select Begin when you are ready for the first question.',
    promptHeading: 'Current question',
    followUpHeading: 'Follow-up question',
    promptWaiting: 'The question will appear here.',
    captionsHeading: 'Captions',
    captionsEmpty: 'Captions of what the interviewer says appear here.',
    enableAudio: 'Click to enable audio',
    enableAudioHint: 'Your browser is blocking sound until you interact with the page.',
    begin: 'Begin',
    pause: 'Pause',
    resume: 'Resume',
    finishAnswer: 'Finish answer',
    finishing: 'Finishing…',
    finishHint: 'Select Finish answer when you are done. Take as long as you need; silence never ends your answer.',
    finishHintText: 'Send your answer, then select Finish answer when you are done with this question.',
    withdraw: 'Withdraw',
    pausedHeading: 'The interview is paused',
    pausedBody:
      'Your microphone is off while the interview is paused. Select Resume when you are ready. A very long pause may end the interview.',
    pausedBodyText: 'Select Resume when you are ready. A very long pause may end the interview.',
    textLabel: 'Your answer',
    textHint: (max: number) =>
      `You can send your answer in one or more parts, up to ${max} characters each. Press Ctrl+Enter or ⌘+Enter to send.`,
    textCounter: (n: number, max: number) => `${n} of ${max} characters`,
    textSend: 'Send',
    textSending: 'Sending…',
    textSent: 'Sent. You can add more, or select Finish answer.',
    micErrors: {
      permission_denied: 'Microphone access is blocked. Allow microphone access for this site in your browser settings.',
      no_device: 'No microphone was found. Connect a microphone.',
      device_in_use: 'Your microphone could not be started. Close other apps that may be using it.',
      insecure_context: 'This browser cannot use the microphone on this page.',
      device_lost: 'Your microphone was disconnected. Reconnect it.',
      unknown: 'Your microphone stopped working. Check that it is connected.',
    },
    micPausedHint: 'We paused the interview for you. When your microphone is ready, select Resume.',
    retryMic: 'Try the microphone again',
    connectionFailed: 'We could not connect to the interview audio.',
    unsupportedBrowser: 'This browser does not support the interview audio. Try a recent version of Chrome, Edge, Firefox or Safari.',
    openedElsewhere: 'This interview was opened in another tab or window, so the audio moved there.',
    useHere: 'Use this window',
    reconnect: 'Reconnect',
    confirmWithdrawTitle: 'Withdraw from the interview?',
    confirmWithdrawBody: 'Withdrawing ends this interview. You will not be able to resume it.',
    confirmWithdrawConfirm: 'Withdraw',
    confirmWithdrawCancel: 'Keep going',
    controlsLabel: 'Interview controls',
  },

  completed: {
    heading: 'Thank you. Your interview is complete.',
    saved: 'Your answers have been saved. You can close this window.',
    nextHeading: 'What happens next',
    noAutomatedDecision:
      'No hiring decision is made automatically. Any automated assessment only supports the people who review your interview.',
    humanReview: 'People at the hiring organisation review your interview before any decision is made.',
    appeal: 'You can ask for a human review of the outcome and appeal it. The person who invited you can tell you how.',
    noScores: 'No scores or results are shown here.',
    closingRemarks: 'The interviewer may say a few closing words.',
  },

  cancelled: {
    heading: 'This interview has ended',
    body: 'No further action is needed in this window.',
    contact: 'If you think this is a mistake, contact the person who invited you.',
  },

  errors: {
    byCode: {
      network_error: 'We could not reach the interview service. Check your internet connection and try again.',
      timeout: 'The interview service took too long to respond. Please try again.',
      invalid_response: 'We received an unexpected response. Please try again.',
      unauthorized:
        'Your interview session has ended or is not valid in this browser. Open your invitation link again to continue.',
      not_found:
        'We could not find this interview for your session. Open your invitation link again to continue.',
      invitation_invalid:
        'This invitation link is no longer valid. It may have expired or been replaced. Contact the person who invited you for a new link.',
      stale_state_version: 'The interview changed while you were acting. The page is up to date now; please try again.',
      transition_not_allowed: 'That action is not available right now. The page is up to date now.',
      conflict: 'That action is not available right now. The page is up to date now.',
      idempotency_key_reused: 'That request was already handled. The page is up to date now.',
      activation_mismatch: 'The question changed before your request arrived. Check the current question.',
      no_pending_request: 'There is no answer waiting to be finished.',
      session_not_live: 'The interview is not in progress.',
      voice_processing_not_consented:
        'Voice mode needs your agreement to the live voice conversation. You can continue by typing your answers.',
      rate_limited: 'Too many attempts. Wait a minute, then try again.',
      payload_too_large: 'That is too long to send. Shorten it and try again.',
      invalid_request: 'That request could not be accepted. Check it and try again.',
      unsupported_media_type: 'That request could not be accepted. Reload the page and try again.',
      idempotency_key_required: 'That request could not be accepted. Reload the page and try again.',
      origin_mismatch: 'This request was blocked for your security. Reload the page and try again.',
      forbidden: 'This action is not allowed.',
      retention_policy_missing: 'This interview is not available right now. Contact the person who invited you.',
      plan_unavailable: 'This interview is not available right now. Contact the person who invited you.',
      unavailable: 'The interview service is temporarily unavailable. Please try again shortly.',
      internal_error: 'Something went wrong on our side. Please try again.',
    } as Record<string, string>,
    byStatus: {
      400: 'That request could not be accepted. Check it and try again.',
      401: 'Your interview session has ended or is not valid in this browser. Open your invitation link again to continue.',
      403: 'This request was blocked. Reload the page and try again.',
      404: 'We could not find this interview for your session. Open your invitation link again to continue.',
      409: 'That action is not available right now. The page is up to date now.',
      410: 'This invitation link is no longer valid. Contact the person who invited you for a new link.',
      413: 'That is too long to send. Shorten it and try again.',
      429: 'Too many attempts. Wait a minute, then try again.',
    } as Record<number, string>,
    serverError: 'The interview service had a problem. Please try again shortly.',
    generic: 'Something went wrong. Please try again.',
  },
};
