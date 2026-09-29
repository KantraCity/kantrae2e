/* C ABI of the mls_ffi crate (see mls-ffi/src/lib.rs). */
#ifndef MLS_FFI_H
#define MLS_FFI_H

#include <stddef.h>
#include <stdint.h>

#define MLS_OK 0
#define MLS_ERR 1
#define MLS_ERR_NOT_FOUND 2
#define MLS_ERR_PANIC 99

#define MLS_KIND_APPLICATION 1
#define MLS_KIND_COMMIT 2
#define MLS_KIND_PROPOSAL 3
#define MLS_KIND_OTHER 4

typedef struct MlsClient MlsClient;

typedef struct {
    uint8_t *ptr;
    size_t len;
    size_t cap;
} MlsBuf;

typedef struct {
    int32_t kind;
    MlsBuf data;
    MlsBuf sender;
    uint64_t epoch;
    int32_t removed;
    int32_t external;
} MlsProcessed;

void mls_buf_free(MlsBuf buf);

int32_t mls_generate_signature_keypair(MlsBuf *out_secret, MlsBuf *out_public, MlsBuf *out_err);
int32_t mls_client_new(const uint8_t *identity, size_t identity_len,
                       const uint8_t *secret, size_t secret_len,
                       const uint8_t *pub, size_t pub_len,
                       MlsClient **out_client, MlsBuf *out_err);
void mls_client_free(MlsClient *c);
int32_t mls_client_load_group(MlsClient *c, const uint8_t *gid, size_t gid_len,
                              const uint8_t *state, size_t state_len,
                              const uint8_t *epochs, size_t epochs_len, MlsBuf *out_err);
int32_t mls_client_load_key_package(MlsClient *c, const uint8_t *id, size_t id_len,
                                    const uint8_t *data, size_t data_len, MlsBuf *out_err);
int32_t mls_client_take_changes(MlsClient *c, MlsBuf *out, MlsBuf *out_err);

int32_t mls_generate_key_package(MlsClient *c, MlsBuf *out, MlsBuf *out_err);
int32_t mls_create_group(MlsClient *c, const uint8_t *gid, size_t gid_len, MlsBuf *out_err);
int32_t mls_create_commit(MlsClient *c, const uint8_t *gid, size_t gid_len,
                          const uint8_t *add_key_packages, size_t add_len,
                          const uint8_t *remove_identities, size_t remove_len,
                          MlsBuf *out_commit, MlsBuf *out_welcome, MlsBuf *out_group_info,
                          MlsBuf *out_err);
int32_t mls_apply_pending_commit(MlsClient *c, const uint8_t *gid, size_t gid_len, MlsBuf *out_err);
int32_t mls_clear_pending_commit(MlsClient *c, const uint8_t *gid, size_t gid_len, MlsBuf *out_err);
int32_t mls_join_group(MlsClient *c, const uint8_t *welcome, size_t welcome_len,
                       MlsBuf *out_gid, MlsBuf *out_err);
int32_t mls_encrypt_application_message(MlsClient *c, const uint8_t *gid, size_t gid_len,
                                        const uint8_t *pt, size_t pt_len,
                                        MlsBuf *out, MlsBuf *out_err);
int32_t mls_process_message(MlsClient *c, const uint8_t *gid, size_t gid_len,
                            const uint8_t *msg, size_t msg_len,
                            MlsProcessed *out, MlsBuf *out_err);
int32_t mls_group_info(MlsClient *c, const uint8_t *gid, size_t gid_len,
                       uint64_t *out_epoch, MlsBuf *out_members, MlsBuf *out_err);
int32_t mls_forget_group(MlsClient *c, const uint8_t *gid, size_t gid_len, MlsBuf *out_err);
int32_t mls_message_info(const uint8_t *msg, size_t msg_len, int32_t *out_kind,
                         uint64_t *out_epoch, MlsBuf *out_gid, MlsBuf *out_err);

int32_t mls_group_info_message(MlsClient *c, const uint8_t *gid, size_t gid_len,
                               MlsBuf *out, MlsBuf *out_err);
int32_t mls_external_join(MlsClient *c, const uint8_t *group_info, size_t group_info_len,
                          MlsBuf *out_gid, MlsBuf *out_commit, MlsBuf *out_group_info,
                          MlsBuf *out_err);
int32_t mls_key_package_identity(const uint8_t *kp, size_t kp_len, MlsBuf *out_identity, MlsBuf *out_err);

#endif
