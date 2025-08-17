const UPLOAD_URL = window.location.origin;
let uploadedFiles = [];
let currentLang = 'en';

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
}

// Initialize language on page load
document.addEventListener('DOMContentLoaded', function() {
    currentLang = detectLanguage();
    switchLanguage(currentLang);
});

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
    // Check file size limit (100MB for Worker free tier)
    const MAX_FILE_SIZE = 5 * 1024 * 1024 * 1024; // 5GB
    if (file.size > MAX_FILE_SIZE) {
        const errorMsg = currentLang === 'zh' 
            ? `文件 ${file.name} 超过 5GB 大小限制。` 
            : `File ${file.name} exceeds 5GB size limit.`;
        showStatus(errorMsg, 'error');
        return;
    }
    
    // Always use simple upload - our Worker handles everything
    uploadSimpleFile(file);
}

function addFileToList(fileName, url) {
    const fileItem = document.createElement('div');
    fileItem.className = 'file-item';
    
    // 添加一次性下载警告
    const warningText = document.createElement('div');
    warningText.style.color = '#ff6b35';
    warningText.style.fontSize = '12px';
    warningText.style.marginBottom = '5px';
    warningText.innerHTML = currentLang === 'zh' 
        ? '⚠️ 注意：此链接只能使用一次，下载后文件将自动删除' 
        : '⚠️ Note: This link can only be used once, file will be deleted after download';
    
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
                    const successMsg = currentLang === 'zh' 
                        ? `成功上传 ${file.name}！(一次性下载)` 
                        : `Successfully uploaded ${file.name}! (One-time download)`;
                    showStatus(successMsg, 'success');
                    // 提取纯URL（去除警告信息）
                    const cleanUrl = responseUrl.split('\n')[0];
                    addFileToList(file.name, cleanUrl);
                    uploadedFiles.push({ name: file.name, url: cleanUrl });
                    return;
                } else {
                    const errorMsg = currentLang === 'zh' 
                        ? '上传完成但收到意外响应' 
                        : 'Upload completed but received unexpected response';
                    throw new Error(errorMsg);
                }
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
        
        
        // Make the request using PUT method to match curl -T behavior
        xhr.open('PUT', `${UPLOAD_URL}/${file.name}`);
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

