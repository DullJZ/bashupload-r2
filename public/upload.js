const UPLOAD_URL = window.location.origin;
let uploadedFiles = [];
let currentLang = 'en';
let serverConfig = null;
let currentUploadMode = 'file'; // 'file' or 'text'

// 格式化字节数为可读字符串
function formatBytes(bytes) {
    if (bytes === 0) return '0B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + sizes[i];
}

// Fetch server configuration
async function fetchServerConfig() {
    try {
        const response = await fetch(`${UPLOAD_URL}/api/config`);
        if (response.ok) {
            serverConfig = await response.json();
            updateMaxExpirationDisplay();
            updateMaxFileSizeDisplay();
            applyPasswordRequirement();
        }
    } catch (error) {
        console.error('Failed to fetch server configuration:', error);
    }
}

// Update maximum expiration time display
function updateMaxExpirationDisplay() {
    if (!serverConfig || !serverConfig.maxAgeForMultiDownload) return;
    
    const maxExpirationSeconds = serverConfig.maxAgeForMultiDownload;
    let value = maxExpirationSeconds;
    let unit = 'seconds';
    let unitZh = '秒';
    
    if (maxExpirationSeconds % 86400 === 0) {
        value = maxExpirationSeconds / 86400;
        unit = 'days';
        unitZh = '天';
    } else if (maxExpirationSeconds % 3600 === 0) {
        value = maxExpirationSeconds / 3600;
        unit = 'hours';
        unitZh = '小时';
    } else if (maxExpirationSeconds % 60 === 0) {
        value = maxExpirationSeconds / 60;
        unit = 'minutes';
        unitZh = '分钟';
    }
    
    const maxExpirationInfo = document.getElementById('maxExpirationInfo');
    if (maxExpirationInfo) {
        const langText = maxExpirationInfo.querySelector('.lang-text');
        if (langText) {
            const maxTextEn = `💡 Maximum allowed: ${value} ${unit}`;
            const maxTextZh = `💡 最大允许值: ${value}${unitZh}`;
            
            langText.setAttribute('data-en', maxTextEn);
            langText.setAttribute('data-zh', maxTextZh);
            langText.textContent = currentLang === 'zh' ? maxTextZh : maxTextEn;
        }
    }
}

// Update maximum file size display
function updateMaxFileSizeDisplay() {
    if (!serverConfig || !serverConfig.maxUploadSize) return;
    
    const maxSizeBytes = serverConfig.maxUploadSize;
    const maxSizeText = formatBytes(maxSizeBytes);
    
    // Update upload area max size display
    const maxFileSizeInfo = document.getElementById('maxFileSizeInfo');
    if (maxFileSizeInfo) {
        const langText = maxFileSizeInfo.querySelector('.lang-text');
        if (langText) {
            const maxTextEn = `Max: ${maxSizeText}`;
            const maxTextZh = `最大: ${maxSizeText}`;
            
            langText.setAttribute('data-en', maxTextEn);
            langText.setAttribute('data-zh', maxTextZh);
            langText.textContent = currentLang === 'zh' ? maxTextZh : maxTextEn;
        }
    }
    
    // Update features section max file size display
    const maxFileSizeFeature = document.getElementById('maxFileSizeFeature');
    if (maxFileSizeFeature) {
        const featureTextEn = `Supports files up to ${maxSizeText} in size`;
        const featureTextZh = `支持最大 ${maxSizeText} 的文件`;
        
        maxFileSizeFeature.setAttribute('data-en', featureTextEn);
        maxFileSizeFeature.setAttribute('data-zh', featureTextZh);
        maxFileSizeFeature.textContent = currentLang === 'zh' ? featureTextZh : featureTextEn;
    }
}

function applyPasswordRequirement() {
    const usePasswordCheckbox = document.getElementById('usePassword');
    const passwordContainer = document.getElementById('passwordContainer');
    const passwordInput = document.getElementById('passwordInput');
    const passwordNotice = document.getElementById('passwordNotice');
    const passwordLabel = usePasswordCheckbox?.closest('.checkbox-label');
    const passwordLabelTexts = passwordLabel ? passwordLabel.querySelectorAll('.lang-text') : null;
    const passwordNoticeText = passwordNotice ? passwordNotice.querySelector('.lang-text') : null;

    if (!usePasswordCheckbox || !passwordContainer || !passwordInput) {
        return;
    }

    const isRequired = Boolean(serverConfig && serverConfig.needPassword);

    // Cache default translations for later restoration
    if (passwordNoticeText) {
        if (!passwordNoticeText.dataset.enDefault) {
            passwordNoticeText.dataset.enDefault = passwordNoticeText.getAttribute('data-en') || '';
        }
        if (!passwordNoticeText.dataset.zhDefault) {
            passwordNoticeText.dataset.zhDefault = passwordNoticeText.getAttribute('data-zh') || '';
        }
    }
    if (passwordLabelTexts && passwordLabelTexts.length > 0) {
        const labelMainText = passwordLabelTexts[0];
        if (labelMainText && !labelMainText.dataset.enDefault) {
            labelMainText.dataset.enDefault = labelMainText.getAttribute('data-en') || '';
        }
        if (labelMainText && !labelMainText.dataset.zhDefault) {
            labelMainText.dataset.zhDefault = labelMainText.getAttribute('data-zh') || '';
        }
        if (passwordLabelTexts.length > 1) {
            const tooltipText = passwordLabelTexts[1];
            if (tooltipText && !tooltipText.dataset.enDefault) {
                tooltipText.dataset.enDefault = tooltipText.getAttribute('data-en') || '';
            }
            if (tooltipText && !tooltipText.dataset.zhDefault) {
                tooltipText.dataset.zhDefault = tooltipText.getAttribute('data-zh') || '';
            }
        }
    }

    if (isRequired) {
        usePasswordCheckbox.checked = true;
        usePasswordCheckbox.disabled = true;
        if (passwordLabel) {
            passwordLabel.classList.add('disabled');
        }
        passwordContainer.style.display = 'block';
        passwordInput.setAttribute('aria-required', 'true');
        updatePasswordPlaceholder();

        if (passwordNoticeText) {
            passwordNoticeText.setAttribute('data-en', '⚠️ Password required: The server enforces uploads to include the configured password');
            passwordNoticeText.setAttribute('data-zh', '⚠️ 必须输入密码：服务器要求上传时提供配置的密码');
            passwordNoticeText.textContent = currentLang === 'zh'
                ? passwordNoticeText.getAttribute('data-zh')
                : passwordNoticeText.getAttribute('data-en');
        }

        if (passwordLabelTexts && passwordLabelTexts.length > 0) {
            const labelMainText = passwordLabelTexts[0];
            labelMainText.setAttribute('data-en', 'Server password required');
            labelMainText.setAttribute('data-zh', '服务器要求必须输入密码');
            labelMainText.textContent = currentLang === 'zh'
                ? labelMainText.getAttribute('data-zh')
                : labelMainText.getAttribute('data-en');

            if (passwordLabelTexts.length > 1) {
                const tooltipText = passwordLabelTexts[1];
                tooltipText.setAttribute('data-en', 'Every upload must include the configured server password');
                tooltipText.setAttribute('data-zh', '每次上传都必须填写服务器配置的密码');
                tooltipText.textContent = currentLang === 'zh'
                    ? tooltipText.getAttribute('data-zh')
                    : tooltipText.getAttribute('data-en');
            }
        }
    } else {
        usePasswordCheckbox.disabled = false;
        if (passwordLabel) {
            passwordLabel.classList.remove('disabled');
        }

        if (passwordNoticeText) {
            const enDefault = passwordNoticeText.dataset.enDefault || passwordNoticeText.getAttribute('data-en') || '';
            const zhDefault = passwordNoticeText.dataset.zhDefault || passwordNoticeText.getAttribute('data-zh') || '';
            passwordNoticeText.setAttribute('data-en', enDefault);
            passwordNoticeText.setAttribute('data-zh', zhDefault);
            passwordNoticeText.textContent = currentLang === 'zh' ? zhDefault : enDefault;
        }

        if (passwordLabelTexts && passwordLabelTexts.length > 0) {
            const labelMainText = passwordLabelTexts[0];
            const enDefault = labelMainText.dataset.enDefault || labelMainText.getAttribute('data-en') || '';
            const zhDefault = labelMainText.dataset.zhDefault || labelMainText.getAttribute('data-zh') || '';
            labelMainText.setAttribute('data-en', enDefault);
            labelMainText.setAttribute('data-zh', zhDefault);
            labelMainText.textContent = currentLang === 'zh' ? zhDefault : enDefault;

            if (passwordLabelTexts.length > 1) {
                const tooltipText = passwordLabelTexts[1];
                const tooltipEnDefault = tooltipText.dataset.enDefault || tooltipText.getAttribute('data-en') || '';
                const tooltipZhDefault = tooltipText.dataset.zhDefault || tooltipText.getAttribute('data-zh') || '';
                tooltipText.setAttribute('data-en', tooltipEnDefault);
                tooltipText.setAttribute('data-zh', tooltipZhDefault);
                tooltipText.textContent = currentLang === 'zh'
                    ? tooltipZhDefault
                    : tooltipEnDefault;
            }
        }

        // Keep password container visibility aligned with current checkbox state
        passwordInput.removeAttribute('aria-required');

        if (usePasswordCheckbox.checked) {
            passwordContainer.style.display = 'block';
            updatePasswordPlaceholder();
        } else {
            passwordContainer.style.display = 'none';
            passwordInput.value = '';
            passwordInput.style.borderColor = '#ddd';
            passwordInput.style.boxShadow = 'none';
        }
    }
}

// Language detection and switching
function detectLanguage() {
    const browserLang = navigator.language || navigator.userLanguage;
    // Check if browser language is Chinese
    if (browserLang.toLowerCase().includes('zh')) {
        return 'zh';
    }
    return 'en';
}

function switchLanguage(lang) {
    currentLang = lang;
    // Update all elements with lang-text class
    document.querySelectorAll('.lang-text').forEach(element => {
        const text = element.getAttribute('data-' + lang);
        if (text) {
            element.textContent = text;
        }
    });
    // Update code blocks
    document.querySelectorAll('.lang-code').forEach(element => {
        const text = element.getAttribute('data-' + lang);
        if (text) {
            element.textContent = text;
        }
    });
    // Update password placeholder if password container is visible
    const passwordContainer = document.getElementById('passwordContainer');
    if (passwordContainer.style.display !== 'none') {
        updatePasswordPlaceholder();
    }
    // Update max expiration display after language switch
    updateMaxExpirationDisplay();
    // Update max file size display after language switch
    updateMaxFileSizeDisplay();
    // Update text input placeholder after language switch
    updateTextInputPlaceholder();
    // Update expiration placeholder after language switch
    updateExpirationPlaceholder();
}

// Switch upload mode between file and text
function switchUploadMode(mode) {
    currentUploadMode = mode;
    const fileContainer = document.getElementById('uploadContainer');
    const textContainer = document.getElementById('textUploadContainer');
    const fileModeBtn = document.getElementById('fileModeBtn');
    const textModeBtn = document.getElementById('textModeBtn');

    if (mode === 'file') {
        fileContainer.style.display = 'block';
        textContainer.style.display = 'none';
        fileModeBtn.classList.add('active');
        textModeBtn.classList.remove('active');
    } else {
        fileContainer.style.display = 'none';
        textContainer.style.display = 'block';
        fileModeBtn.classList.remove('active');
        textModeBtn.classList.add('active');
    }
}

// Handle text upload
function handleTextUpload() {
    const textInput = document.getElementById('textInput');
    const text = textInput.value.trim();

    if (!text) {
        const errorMsg = currentLang === 'zh'
            ? '请输入要分享的文本内容'
            : 'Please enter text content to share';
        showStatus(errorMsg, 'error');
        return;
    }

    // Check if password protection is enabled but no password provided
    const usePassword = document.getElementById('usePassword')?.checked;
    const requirePassword = Boolean(serverConfig?.needPassword);
    const passwordInput = document.getElementById('passwordInput');
    if ((requirePassword || usePassword) && (!passwordInput || !passwordInput.value.trim())) {
        const errorMsg = currentLang === 'zh'
            ? '请输入密码以启用密码保护。'
            : 'Please enter a password to enable password protection.';
        showStatus(errorMsg, 'error');
        return;
    }

    uploadText(text);
}

// Upload text content
async function uploadText(text, maxRetries = 3) {
    const uploadingMsg = currentLang === 'zh'
        ? '正在上传文本...'
        : 'Uploading text...';
    showStatus(uploadingMsg, 'uploading');
    showProgress();

    let lastError;
    for (let attempt = 1; attempt <= maxRetries; attempt++) {
        try {
            const response = await uploadTextWithProgress(text, (progress) => {
                updateProgress(progress);
            });

            if (response.status === 200) {
                const responseUrl = response.responseText.trim();
                if (responseUrl.startsWith('http')) {
                    hideProgress();
                    // 根据是否设置了有效期显示不同的成功消息
                    const useExpiration = document.getElementById('useExpiration')?.checked;
                    let successMsg;
                    if (useExpiration) {
                        const setExpirationBtn = document.getElementById('setExpirationBtn');
                        const expirationSeconds = setExpirationBtn.getAttribute('data-expiration-seconds');

                        if (expirationSeconds) {
                            const seconds = parseInt(expirationSeconds);
                            let value = seconds;
                            let unit = 'seconds';

                            if (seconds % 86400 === 0) {
                                value = seconds / 86400;
                                unit = 'days';
                            } else if (seconds % 3600 === 0) {
                                value = seconds / 3600;
                                unit = 'hours';
                            } else if (seconds % 60 === 0) {
                                value = seconds / 60;
                                unit = 'minutes';
                            }

                            let expirationString = currentLang === 'zh'
                                ? `${value}${unit}`
                                : `${value} ${unit}`;

                            successMsg = currentLang === 'zh'
                                ? `文本上传成功！(有效期: ${expirationString})`
                                : `Text uploaded successfully! (Expiration: ${expirationString})`;
                        } else {
                            successMsg = currentLang === 'zh'
                                ? `文本上传成功！(默认有效期: 1小时)`
                                : `Text uploaded successfully! (Default expiration: 1 hour)`;
                        }
                    } else {
                        successMsg = currentLang === 'zh'
                            ? `文本上传成功！(一次性下载)`
                            : `Text uploaded successfully! (One-time download)`;
                    }
                    showStatus(successMsg, 'success');
                    // 提取纯URL（去除警告信息）
                    const cleanUrl = responseUrl.split('\n')[0];
                    const usePassword = document.getElementById('usePassword')?.checked;
                    addFileToList('text.txt', cleanUrl, usePassword);
                    uploadedFiles.push({ name: 'text.txt', url: cleanUrl, passwordProtected: usePassword });

                    // Clear text input after successful upload
                    document.getElementById('textInput').value = '';
                    return;
                } else {
                    const errorMsg = currentLang === 'zh'
                        ? '上传完成但收到意外响应'
                        : 'Upload completed but received unexpected response';
                    throw new Error(errorMsg);
                }
            } else if (response.status === 401) {
                hideProgress();
                const passwordErrorMsg = currentLang === 'zh'
                    ? '密码错误，请检查您输入的密码是否与服务器配置的PASSWORD环境变量相同'
                    : 'Password error, please check that the password you entered matches the PASSWORD environment variable configured on the server';
                showStatus(passwordErrorMsg, 'error');
                return;
            } else {
                throw new Error(`Server returned status ${response.status}`);
            }
        } catch (error) {
            lastError = error;
            console.log(`Upload failed (attempt ${attempt}/${maxRetries}):`, error);

            if (attempt < maxRetries) {
                const delay = Math.pow(2, attempt - 1) * 1000;
                console.log(`Retrying in ${delay/1000} seconds...`);
                const retryMsg = currentLang === 'zh'
                    ? `上传失败，${delay/1000} 秒后重试...`
                    : `Upload failed, retrying in ${delay/1000} seconds...`;
                showStatus(retryMsg, 'uploading');
                await new Promise(resolve => setTimeout(resolve, delay));
            }
        }
    }

    hideProgress();
    const failedMsg = currentLang === 'zh'
        ? `上传文本失败，已重试 ${maxRetries} 次。错误：${lastError.message}`
        : `Failed to upload text after ${maxRetries} attempts. Error: ${lastError.message}`;
    showStatus(failedMsg, 'error');
}

// Upload text with progress tracking
function uploadTextWithProgress(text, onProgress) {
    return new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();

        // Upload progress
        xhr.upload.addEventListener('progress', function(e) {
            if (e.lengthComputable) {
                const percentComplete = (e.loaded / e.total) * 100;
                onProgress(percentComplete);
            }
        });

        // Upload complete
        xhr.addEventListener('load', function() {
            resolve({
                status: xhr.status,
                responseText: xhr.responseText
            });
        });

        // Upload error
        xhr.addEventListener('error', function() {
            reject(new Error('Network error occurred'));
        });

        // Check if short URL option is selected
        const useShortUrl = document.getElementById('useShortUrl')?.checked;
        const uploadPath = useShortUrl ? `${UPLOAD_URL}/short` : `${UPLOAD_URL}/`;

        // Check if password protection is enabled
        const usePassword = document.getElementById('usePassword')?.checked;
        const passwordInput = document.getElementById('passwordInput');

        // Check if expiration is set
        const useExpiration = document.getElementById('useExpiration')?.checked;

        // Use POST method for text upload
        xhr.open('POST', uploadPath);
        xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded');

        // Add Authorization header if password is provided
        if (usePassword && passwordInput.value) {
            xhr.setRequestHeader('Authorization', passwordInput.value);
        }

        // Add expiration header if expiration is set
        if (useExpiration) {
            const setExpirationBtn = document.getElementById('setExpirationBtn');
            let expirationSeconds = setExpirationBtn.getAttribute('data-expiration-seconds');

            // If no expiration time was explicitly set, use default (1 hour)
            if (!expirationSeconds) {
                expirationSeconds = '3600';
            }

            xhr.setRequestHeader('X-Expiration-Seconds', expirationSeconds);
        }

        // Send text as form data
        xhr.send(text);
    });
}

// Initialize language on page load
document.addEventListener('DOMContentLoaded', function() {
    currentLang = detectLanguage();
    switchLanguage(currentLang);
    
    // Fetch server configuration
    fetchServerConfig();
    
    // Set up password checkbox functionality
    const usePasswordCheckbox = document.getElementById('usePassword');
    const passwordContainer = document.getElementById('passwordContainer');
    const passwordInput = document.getElementById('passwordInput');
    
    usePasswordCheckbox.addEventListener('change', function() {
        if (this.checked) {
            passwordContainer.style.display = 'block';
            // Update placeholder text based on language
            updatePasswordPlaceholder();
            // Focus on password input
            setTimeout(() => passwordInput.focus(), 100);
        } else {
            passwordContainer.style.display = 'none';
            passwordInput.value = '';
        }
    });
    
    // Add visual feedback for password input
    passwordInput.addEventListener('input', function() {
        if (this.value.trim()) {
            this.style.borderColor = '#28a745';
            this.style.boxShadow = '0 0 0 2px rgba(40, 167, 69, 0.1)';
        } else {
            this.style.borderColor = '#ddd';
            this.style.boxShadow = 'none';
        }
    });

    // Set up expiration checkbox functionality
    const useExpirationCheckbox = document.getElementById('useExpiration');
    const expirationContainer = document.getElementById('expirationContainer');
    
    useExpirationCheckbox.addEventListener('change', function() {
        if (this.checked) {
            expirationContainer.style.display = 'block';
        } else {
            expirationContainer.style.display = 'none';
        }
    });

    // Set up expiration button functionality
    const setExpirationBtn = document.getElementById('setExpirationBtn');
    const expirationResult = document.getElementById('expirationResult');
    const expirationInput = document.getElementById('expirationInput');
    const expirationUnit = document.getElementById('expirationUnit');
    
    setExpirationBtn.addEventListener('click', function() {
        const value = parseInt(expirationInput.value);
        const unit = expirationUnit.value;
        
        if (value > 0) {
            // Store expiration time in seconds in a global/alternative way
            let seconds = 0;
            switch(unit) {
                case 'seconds':
                    seconds = value;
                    break;
                case 'minutes':
                    seconds = value * 60;
                    break;
                case 'hours':
                    seconds = value * 3600;
                    break;
                case 'days':
                    seconds = value * 86400;
                    break;
            }
            
            // Store in a data attribute for reliable access
            setExpirationBtn.setAttribute('data-expiration-seconds', seconds);
            
            // Show success message
            expirationResult.style.display = 'block';
            setTimeout(() => {
                expirationResult.style.display = 'none';
            }, 2000);
        }
    });

    // Update expiration placeholder based on language
    updateExpirationPlaceholder();

    // Update text input placeholder based on language
    updateTextInputPlaceholder();
});

function updateTextInputPlaceholder() {
    const textInput = document.getElementById('textInput');
    if (textInput) {
        const enPlaceholder = textInput.getAttribute('data-en-placeholder');
        const zhPlaceholder = textInput.getAttribute('data-zh-placeholder');
        textInput.placeholder = currentLang === 'zh' ? zhPlaceholder : enPlaceholder;
    }
}

function updatePasswordPlaceholder() {
    const passwordInput = document.getElementById('passwordInput');
    const enPlaceholder = passwordInput.getAttribute('data-en-placeholder');
    const zhPlaceholder = passwordInput.getAttribute('data-zh-placeholder');
    passwordInput.placeholder = currentLang === 'zh' ? zhPlaceholder : enPlaceholder;
    
    // Update password notice text
    const passwordNotice = document.getElementById('passwordNotice');
    if (passwordNotice) {
        const noticeText = passwordNotice.querySelector('.lang-text');
        if (noticeText) {
            const enText = noticeText.getAttribute('data-en');
            const zhText = noticeText.getAttribute('data-zh');
            noticeText.textContent = currentLang === 'zh' ? zhText : enText;
        }
    }
}

function updateExpirationPlaceholder() {
    const expirationInput = document.getElementById('expirationInput');
    const enPlaceholder = expirationInput.getAttribute('data-en-placeholder');
    const zhPlaceholder = expirationInput.getAttribute('data-zh-placeholder');
    expirationInput.placeholder = currentLang === 'zh' ? zhPlaceholder : enPlaceholder;
    
    // Update expiration units text
    const expirationUnit = document.getElementById('expirationUnit');
    const optionSeconds = expirationUnit.querySelector('option[value="seconds"]');
    const optionMinutes = expirationUnit.querySelector('option[value="minutes"]');
    const optionHours = expirationUnit.querySelector('option[value="hours"]');
    const optionDays = expirationUnit.querySelector('option[value="days"]');
    
    if (currentLang === 'zh') {
        optionSeconds.textContent = '秒';
        optionMinutes.textContent = '分钟';
        optionHours.textContent = '小时';
        optionDays.textContent = '天';
    } else {
        optionSeconds.textContent = 'seconds';
        optionMinutes.textContent = 'minutes';
        optionHours.textContent = 'hours';
        optionDays.textContent = 'days';
    }
}

// Get DOM elements
const uploadContainer = document.getElementById('uploadContainer');
const uploadStatus = document.getElementById('uploadStatus');
const progressBar = document.getElementById('progressBar');
const progressFill = document.getElementById('progressFill');
const fileList = document.getElementById('fileList');

// Drag and drop event handlers
uploadContainer.addEventListener('dragover', handleDragOver);
uploadContainer.addEventListener('dragleave', handleDragLeave);
uploadContainer.addEventListener('drop', handleDrop);

function handleDragOver(e) {
    e.preventDefault();
    e.stopPropagation();
    uploadContainer.classList.add('dragging');
}

function handleDragLeave(e) {
    e.preventDefault();
    e.stopPropagation();
    uploadContainer.classList.remove('dragging');
}

function handleDrop(e) {
    e.preventDefault();
    e.stopPropagation();
    uploadContainer.classList.remove('dragging');
    
    const files = e.dataTransfer.files;
    if (files.length > 0) {
        handleFiles(files);
    }
}

function handleFileSelect(e) {
    const files = e.target.files;
    if (files.length > 0) {
        handleFiles(files);
    }
}

function handleFiles(files) {
    for (let i = 0; i < files.length; i++) {
        uploadFile(files[i]);
    }
}

function showStatus(message, type) {
    uploadStatus.textContent = message;
    uploadStatus.className = 'upload-status ' + type;
    uploadStatus.style.display = 'block';
}

function hideStatus() {
    uploadStatus.style.display = 'none';
}

function showProgress() {
    progressBar.style.display = 'block';
    progressFill.style.width = '0%';
}

function updateProgress(percent) {
    progressFill.style.width = percent + '%';
}

function hideProgress() {
    progressBar.style.display = 'none';
}

// Simplified upload function - our Worker handles everything with a single PUT request

async function uploadFile(file) {
    // Check file size limit using server configuration
    const maxFileSize = serverConfig?.maxUploadSize || (5 * 1024 * 1024 * 1024); // Default 5GB if config not loaded
    if (file.size > maxFileSize) {
        const maxSizeText = formatBytes(maxFileSize);
        const errorMsg = currentLang === 'zh' 
            ? `文件 ${file.name} 超过 ${maxSizeText} 大小限制。` 
            : `File ${file.name} exceeds ${maxSizeText} size limit.`;
        showStatus(errorMsg, 'error');
        return;
    }
    
    // Check if password protection is enabled but no password provided
    const usePassword = document.getElementById('usePassword')?.checked;
    const requirePassword = Boolean(serverConfig?.needPassword);
    const passwordInput = document.getElementById('passwordInput');
    if ((requirePassword || usePassword) && (!passwordInput || !passwordInput.value.trim())) {
        const errorMsg = currentLang === 'zh' 
            ? '请输入密码以启用密码保护。' 
            : 'Please enter a password to enable password protection.';
        showStatus(errorMsg, 'error');
        return;
    }
    
    // Always use simple upload - our Worker handles everything
    uploadSimpleFile(file);
}

function addFileToList(fileName, url, usePassword = false) {
    const fileItem = document.createElement('div');
    fileItem.className = 'file-item';
    
    // 检查是否设置了有效期
    const useExpiration = document.getElementById('useExpiration')?.checked;
    
    // 添加下载警告（根据是否有有效期显示不同的消息）
    const warningText = document.createElement('div');
    warningText.style.color = '#ff6b35';
    warningText.style.fontSize = '12px';
    warningText.style.marginBottom = '5px';
    if (useExpiration) {
        const setExpirationBtn = document.getElementById('setExpirationBtn');
        const expirationSeconds = setExpirationBtn.getAttribute('data-expiration-seconds');
        
        if (expirationSeconds) {
            // Convert seconds to human readable format
            const seconds = parseInt(expirationSeconds);
            let value = seconds;
            let unit = 'seconds';
            
            if (seconds % 86400 === 0) {
                value = seconds / 86400;
                unit = 'days';
            } else if (seconds % 3600 === 0) {
                value = seconds / 3600;
                unit = 'hours';
            } else if (seconds % 60 === 0) {
                value = seconds / 60;
                unit = 'minutes';
            }
            
            let expirationString = currentLang === 'zh' 
                ? `${value}${unit}` 
                : `${value} ${unit}`;
            
            warningText.innerHTML = currentLang === 'zh' 
                ? `🕐 注意：此文件将在 ${expirationString} 后过期，期间可多次下载` 
                : `🕐 Note: This file will expire after ${expirationString} and can be downloaded multiple times`;
        } else {
            // Default to 1 hour if no specific time was set
            let expirationString = currentLang === 'zh' ? '1小时' : '1 hour';
            warningText.innerHTML = currentLang === 'zh' 
                ? `🕐 注意：此文件将在 ${expirationString} 后过期，期间可多次下载` 
                : `🕐 Note: This file will expire after ${expirationString} and can be downloaded multiple times`;
        }
    } else {
        warningText.innerHTML = currentLang === 'zh' 
            ? '⚠️ 注意：此链接只能使用一次，下载后文件将自动删除' 
            : '⚠️ Note: This link can only be used once, file will be deleted after download';
    }
    
    // 添加密码保护警告
    let passwordWarning = null;
    if (usePassword) {
        passwordWarning = document.createElement('div');
        passwordWarning.style.color = '#e74c3c';
        passwordWarning.style.fontSize = '12px';
        passwordWarning.style.marginBottom = '5px';
        passwordWarning.innerHTML = currentLang === 'zh' 
            ? '🔒 注意：此链接需要密码才能下载' 
            : '🔒 Note: This link requires a password to download';
    }
    
    const fileUrl = document.createElement('span');
    fileUrl.className = 'file-url';
    fileUrl.innerHTML = `<strong>${fileName}:</strong> <a href="${url}" target="_blank">${url}</a>`;
    
    const copyButton = document.createElement('button');
    copyButton.className = 'copy-button';
    copyButton.textContent = currentLang === 'zh' ? '复制链接' : 'Copy URL';
    copyButton.onclick = function() {
        copyToClipboard(url);
        copyButton.textContent = currentLang === 'zh' ? '已复制！' : 'Copied!';
        setTimeout(() => {
            copyButton.textContent = currentLang === 'zh' ? '复制链接' : 'Copy URL';
        }, 2000);
    };
    
    fileItem.appendChild(warningText);
    if (passwordWarning) {
        fileItem.appendChild(passwordWarning);
    }
    fileItem.appendChild(fileUrl);
    fileItem.appendChild(copyButton);
    fileList.appendChild(fileItem);
}

async function uploadSimpleFile(file, maxRetries = 3) {
    const uploadingMsg = currentLang === 'zh' 
        ? `正在上传 ${file.name}...` 
        : `Uploading ${file.name}...`;
    showStatus(uploadingMsg, 'uploading');
    showProgress();
    
    let lastError;
    for (let attempt = 1; attempt <= maxRetries; attempt++) {
        try {
            const response = await uploadWithProgress(file, (progress) => {
                updateProgress(progress);
            });
            
            if (response.status === 200) {
                const responseUrl = response.responseText.trim();
                if (responseUrl.startsWith('http')) {
                    hideProgress();
                    // 根据是否设置了有效期显示不同的成功消息
                    const useExpiration = document.getElementById('useExpiration')?.checked;
                    let successMsg;
                    if (useExpiration) {
                        const setExpirationBtn = document.getElementById('setExpirationBtn');
                        const expirationSeconds = setExpirationBtn.getAttribute('data-expiration-seconds');
                        
                        if (expirationSeconds) {
                            // Convert seconds to human readable format
                            const seconds = parseInt(expirationSeconds);
                            let value = seconds;
                            let unit = 'seconds';
                            
                            if (seconds % 86400 === 0) {
                                value = seconds / 86400;
                                unit = 'days';
                            } else if (seconds % 3600 === 0) {
                                value = seconds / 3600;
                                unit = 'hours';
                            } else if (seconds % 60 === 0) {
                                value = seconds / 60;
                                unit = 'minutes';
                            }
                            
                            let expirationString = currentLang === 'zh' 
                                ? `${value}${unit}` 
                                : `${value} ${unit}`;
                            
                            successMsg = currentLang === 'zh' 
                                ? `成功上传 ${file.name}！(有效期: ${expirationString})` 
                                : `Successfully uploaded ${file.name}! (Expiration: ${expirationString})`;
                        } else {
                            successMsg = currentLang === 'zh' 
                                ? `成功上传 ${file.name}！(默认有效期: 1小时)` 
                                : `Successfully uploaded ${file.name}! (Default expiration: 1 hour)`;
                        }
                    } else {
                        successMsg = currentLang === 'zh' 
                            ? `成功上传 ${file.name}！(一次性下载)` 
                            : `Successfully uploaded ${file.name}! (One-time download)`;
                    }
                    showStatus(successMsg, 'success');
                    // 提取纯URL（去除警告信息）
                    const cleanUrl = responseUrl.split('\n')[0];
                    const usePassword = document.getElementById('usePassword')?.checked;
                    addFileToList(file.name, cleanUrl, usePassword);
                    uploadedFiles.push({ name: file.name, url: cleanUrl, passwordProtected: usePassword });
                    return;
                } else {
                    const errorMsg = currentLang === 'zh' 
                        ? '上传完成但收到意外响应' 
                        : 'Upload completed but received unexpected response';
                    throw new Error(errorMsg);
                }
            } else if (response.status === 401) {
                // Handle password error specifically
                hideProgress();
                const passwordErrorMsg = currentLang === 'zh' 
                    ? '密码错误，请检查您输入的密码是否与服务器配置的PASSWORD环境变量相同' 
                    : 'Password error, please check that the password you entered matches the PASSWORD environment variable configured on the server';
                showStatus(passwordErrorMsg, 'error');
                return;
            } else {
                throw new Error(`Server returned status ${response.status}`);
            }
        } catch (error) {
            lastError = error;
            console.log(`Upload failed (attempt ${attempt}/${maxRetries}):`, error);
            
            if (attempt < maxRetries) {
                const delay = Math.pow(2, attempt - 1) * 1000;
                console.log(`Retrying in ${delay/1000} seconds...`);
                const retryMsg = currentLang === 'zh' 
                    ? `上传失败，${delay/1000} 秒后重试...` 
                    : `Upload failed, retrying in ${delay/1000} seconds...`;
                showStatus(retryMsg, 'uploading');
                await new Promise(resolve => setTimeout(resolve, delay));
            }
        }
    }
    
    hideProgress();
    const failedMsg = currentLang === 'zh' 
        ? `上传 ${file.name} 失败，已重试 ${maxRetries} 次。错误：${lastError.message}` 
        : `Failed to upload ${file.name} after ${maxRetries} attempts. Error: ${lastError.message}`;
    showStatus(failedMsg, 'error');
}

function uploadWithProgress(file, onProgress) {
    return new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        
        // Upload progress
        xhr.upload.addEventListener('progress', function(e) {
            if (e.lengthComputable) {
                const percentComplete = (e.loaded / e.total) * 100;
                onProgress(percentComplete);
            }
        });

        // Upload complete
        xhr.addEventListener('load', function() {
            resolve({
                status: xhr.status,
                responseText: xhr.responseText
            });
        });

        // Upload error
        xhr.addEventListener('error', function() {
            reject(new Error('Network error occurred'));
        });
        
        // Check if short URL option is selected
        const useShortUrl = document.getElementById('useShortUrl')?.checked;
        const uploadPath = useShortUrl ? `${UPLOAD_URL}/short` : `${UPLOAD_URL}/${file.name}`;
        
        // Check if password protection is enabled
        const usePassword = document.getElementById('usePassword')?.checked;
        const passwordInput = document.getElementById('passwordInput');
        
        // Check if expiration is set
        const useExpiration = document.getElementById('useExpiration')?.checked;
        
        // Make the request using PUT method to match curl -T behavior
        xhr.open('PUT', uploadPath);
        
        // Add Authorization header if password is provided
        if (usePassword && passwordInput.value) {
            xhr.setRequestHeader('Authorization', passwordInput.value);
        }
        
        // Add expiration header if expiration is set
        if (useExpiration) {
            const setExpirationBtn = document.getElementById('setExpirationBtn');
            let expirationSeconds = setExpirationBtn.getAttribute('data-expiration-seconds');
            
            // If no expiration time was explicitly set, use default (1 hour)
            if (!expirationSeconds) {
                // Default to 1 hour (3600 seconds)
                expirationSeconds = '3600';
            }
            
            xhr.setRequestHeader('X-Expiration-Seconds', expirationSeconds);
        }
        
        xhr.send(file);
    });
}

function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text);
    } else {
        // Fallback for older browsers
        const textArea = document.createElement('textarea');
        textArea.value = text;
        textArea.style.position = 'fixed';
        textArea.style.opacity = '0';
        document.body.appendChild(textArea);
        textArea.focus();
        textArea.select();
        document.execCommand('copy');
        document.body.removeChild(textArea);
    }
}
