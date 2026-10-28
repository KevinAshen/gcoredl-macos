//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

static OSStatus gcoredl_keychain_set(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen,
    const void *password, UInt32 passwordLen) {
    SecKeychainItemRef item = NULL;
    OSStatus status = SecKeychainFindGenericPassword(
        NULL, serviceLen, service, accountLen, account,
        NULL, NULL, &item);
    if (status == errSecSuccess) {
        status = SecKeychainItemModifyAttributesAndData(item, NULL, passwordLen, password);
        CFRelease(item);
        return status;
    }
    if (item != NULL) {
        CFRelease(item);
    }
    if (status != errSecItemNotFound) {
        return status;
    }
    return SecKeychainAddGenericPassword(
        NULL, serviceLen, service, accountLen, account,
        passwordLen, password, NULL);
}

static OSStatus gcoredl_keychain_get(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen,
    void **outPassword, UInt32 *outPasswordLen) {
    void *passwordData = NULL;
    UInt32 passwordLen = 0;
    OSStatus status = SecKeychainFindGenericPassword(
        NULL, serviceLen, service, accountLen, account,
        &passwordLen, &passwordData, NULL);
    if (status != errSecSuccess) {
        return status;
    }
    void *copy = malloc(passwordLen);
    if (copy == NULL && passwordLen > 0) {
        SecKeychainItemFreeContent(NULL, passwordData);
        return errSecAllocate;
    }
    if (passwordLen > 0) {
        memcpy(copy, passwordData, passwordLen);
    }
    SecKeychainItemFreeContent(NULL, passwordData);
    *outPassword = copy;
    *outPasswordLen = passwordLen;
    return errSecSuccess;
}

static OSStatus gcoredl_keychain_delete(
    const void *service, UInt32 serviceLen,
    const void *account, UInt32 accountLen) {
    SecKeychainItemRef item = NULL;
    OSStatus status = SecKeychainFindGenericPassword(
        NULL, serviceLen, service, accountLen, account,
        NULL, NULL, &item);
    if (status != errSecSuccess) {
        return status;
    }
    status = SecKeychainItemDelete(item);
    CFRelease(item);
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
