//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>
#include <stdlib.h>

static CFMutableDictionaryRef gcoredl_keychain_query(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen) {
    CFStringRef serviceString = CFStringCreateWithBytes(
        kCFAllocatorDefault, service, serviceLen, kCFStringEncodingUTF8, 0);
    CFStringRef accountString = CFStringCreateWithBytes(
        kCFAllocatorDefault, account, accountLen, kCFStringEncodingUTF8, 0);
    if (serviceString == NULL || accountString == NULL) {
        if (serviceString != NULL) CFRelease(serviceString);
        if (accountString != NULL) CFRelease(accountString);
        return NULL;
    }

    CFMutableDictionaryRef query = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    if (query != NULL) {
        CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
        CFDictionarySetValue(query, kSecAttrService, serviceString);
        CFDictionarySetValue(query, kSecAttrAccount, accountString);
    }
    CFRelease(serviceString);
    CFRelease(accountString);
    return query;
}

static OSStatus gcoredl_keychain_set(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen,
    const void *password, UInt32 passwordLen) {
    CFMutableDictionaryRef query = gcoredl_keychain_query(
        service, serviceLen, account, accountLen);
    CFDataRef passwordData = CFDataCreate(
        kCFAllocatorDefault, password, passwordLen);
    if (query == NULL || passwordData == NULL) {
        if (query != NULL) CFRelease(query);
        if (passwordData != NULL) CFRelease(passwordData);
        return errSecAllocate;
    }

    CFMutableDictionaryRef updates = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    if (updates == NULL) {
        CFRelease(passwordData);
        CFRelease(query);
        return errSecAllocate;
    }
    CFDictionarySetValue(updates, kSecValueData, passwordData);

    OSStatus status = SecItemUpdate(query, updates);
    if (status == errSecItemNotFound) {
        CFDictionarySetValue(query, kSecValueData, passwordData);
        status = SecItemAdd(query, NULL);
    }

    CFRelease(updates);
    CFRelease(passwordData);
    CFRelease(query);
    return status;
}

static OSStatus gcoredl_keychain_get(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen,
    void **outPassword, UInt32 *outPasswordLen) {
    *outPassword = NULL;
    *outPasswordLen = 0;
    CFMutableDictionaryRef query = gcoredl_keychain_query(
        service, serviceLen, account, accountLen);
    if (query == NULL) {
        return errSecAllocate;
    }
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);

    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (status != errSecSuccess) {
        return status;
    }
    if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
        if (result != NULL) CFRelease(result);
        return errSecDecode;
    }

    CFDataRef passwordData = (CFDataRef)result;
    CFIndex passwordLen = CFDataGetLength(passwordData);
    if (passwordLen < 0 || (uint64_t)passwordLen > UINT32_MAX) {
        CFRelease(passwordData);
        return errSecAllocate;
    }
    void *copy = malloc((size_t)passwordLen);
    if (copy == NULL && passwordLen > 0) {
        CFRelease(passwordData);
        return errSecAllocate;
    }
    if (passwordLen > 0) {
        CFDataGetBytes(passwordData, CFRangeMake(0, passwordLen), copy);
    }
    CFRelease(passwordData);
    *outPassword = copy;
    *outPasswordLen = (UInt32)passwordLen;
    return errSecSuccess;
}

static OSStatus gcoredl_keychain_delete(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen) {
    CFMutableDictionaryRef query = gcoredl_keychain_query(
        service, serviceLen, account, accountLen);
    if (query == NULL) {
        return errSecAllocate;
    }
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    return status;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const errSecItemNotFoundCode = -25300

func keychainSet(service, account, password string) error {
	serviceData := C.CBytes([]byte(service))
	accountData := C.CBytes([]byte(account))
	passwordData := C.CBytes([]byte(password))
	defer C.free(serviceData)
	defer C.free(accountData)
	defer C.free(passwordData)
	status := C.gcoredl_keychain_set(
		serviceData, C.UInt32(len(service)),
		accountData, C.UInt32(len(account)),
		passwordData, C.UInt32(len(password)),
	)
	return keychainStatusError("写入", status)
}

func keychainGet(service, account string) (string, error) {
	serviceData := C.CBytes([]byte(service))
	accountData := C.CBytes([]byte(account))
	defer C.free(serviceData)
	defer C.free(accountData)
	var passwordData unsafe.Pointer
	var passwordLen C.UInt32
	status := C.gcoredl_keychain_get(
		serviceData, C.UInt32(len(service)),
		accountData, C.UInt32(len(account)),
		&passwordData, &passwordLen,
	)
	if err := keychainStatusError("读取", status); err != nil {
		return "", err
	}
	defer C.free(passwordData)
	return string(C.GoBytes(passwordData, C.int(passwordLen))), nil
}

func keychainDelete(service, account string) error {
	serviceData := C.CBytes([]byte(service))
	accountData := C.CBytes([]byte(account))
	defer C.free(serviceData)
	defer C.free(accountData)
	status := C.gcoredl_keychain_delete(
		serviceData, C.UInt32(len(service)),
		accountData, C.UInt32(len(account)),
	)
	return keychainStatusError("删除", status)
}

func keychainStatusError(operation string, status C.OSStatus) error {
	code := int32(status)
	if code == 0 {
		return nil
	}
	if code == errSecItemNotFoundCode {
		return errCredentialNotFound
	}
	return fmt.Errorf("%s钥匙串项目失败（OSStatus %d）", operation, code)
}
