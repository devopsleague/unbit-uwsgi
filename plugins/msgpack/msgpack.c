#include <uwsgi.h>

extern struct uwsgi_server uwsgi;

/*

	msgpack utilities

*/

enum {
	MSGPACK_NIL = 0,
	MSGPACK_TRUE,
	MSGPACK_FALSE,
	MSGPACK_INT,
	MSGPACK_FLOAT,
	MSGPACK_STR,
	MSGPACK_BIN,
	MSGPACK_ARRAY,
	MSGPACK_MAP,
	MSGPACK_MAGIC,
};

struct uwsgi_msgpack_item {
	uint8_t type;
	char *str;
	uint32_t str_len;
	int64_t num;
	int (*func)(char *);
	struct uwsgi_msgpack_item *next;
};

struct uwsgi_msgpack_item *uwsgi_msgpack_item_add(struct uwsgi_msgpack_item **list, uint8_t type) {
	struct uwsgi_msgpack_item *old_umi = NULL, *umi = *list;
	while(umi) {
		old_umi = umi;
		umi = umi->next;
	}

	umi = uwsgi_calloc(sizeof(struct uwsgi_msgpack_item)); 
	umi->type = type;
	if (old_umi) {
		old_umi->next = umi;
	}
	else {
		*list = umi;
	}
	return umi;
}

int uwsgi_buffer_msgpack_map(struct uwsgi_buffer *ub, uint32_t len) {
	if (len <= 15) {
		return uwsgi_buffer_byte(ub, 0x80 + len);
	}
	else if (len <= 0xffff) {
		if (uwsgi_buffer_byte(ub, 0xDE)) return -1;
		return uwsgi_buffer_u16be(ub, (uint16_t) len);
	}
	
	if (uwsgi_buffer_byte(ub, 0xDF)) return -1;
	return uwsgi_buffer_u32be(ub, len);
}

int uwsgi_buffer_msgpack_array(struct uwsgi_buffer *ub, uint32_t len) {
        if (len <= 15) {
                return uwsgi_buffer_byte(ub, 0x90 + len);
        }
        else if (len <= 0xffff) {
                if (uwsgi_buffer_byte(ub, 0xDC)) return -1;
                return uwsgi_buffer_u16be(ub, (uint16_t) len);
        }

        if (uwsgi_buffer_byte(ub, 0xDD)) return -1;
        return uwsgi_buffer_u32be(ub, len);
}

int uwsgi_buffer_msgpack_str(struct uwsgi_buffer *ub, char *str, uint32_t len) {
        if (len <= 31) {
                if (uwsgi_buffer_byte(ub, 0xA0 + len)) return -1;
        }
	// this is annoying, D9 does not work for older SPEC
/*
        else if (len <= 0xff) {
                if (uwsgi_buffer_byte(ub, 0xD9)) return -1;
                if (uwsgi_buffer_byte(ub, (uint8_t) len)) return -1;
        }
*/
	else if (len <= 0xffff) {
                if (uwsgi_buffer_byte(ub, 0xDA)) return -1;
                if (uwsgi_buffer_u16be(ub, (uint16_t) len)) return -1;
	}
	else {
                if (uwsgi_buffer_byte(ub, 0xDB)) return -1;
                if (uwsgi_buffer_u32be(ub, len)) return -1;
	}

        return uwsgi_buffer_append(ub, str, len);
}

int uwsgi_buffer_msgpack_bin(struct uwsgi_buffer *ub, char *str, uint32_t len) {
        if (len <= 0xff) {
                if (uwsgi_buffer_byte(ub, 0xC4)) return -1;
                if (uwsgi_buffer_byte(ub, (uint8_t) len)) return -1;
        }
        else if (len <= 0xffff) {
                if (uwsgi_buffer_byte(ub, 0xC5)) return -1;
		if (uwsgi_buffer_u16be(ub, (uint16_t) len)) return -1;
        }
        else {
                if (uwsgi_buffer_byte(ub, 0xC6)) return -1;
		if (uwsgi_buffer_u32be(ub, len)) return -1;
        }

        return uwsgi_buffer_append(ub, str, len);
}


int uwsgi_buffer_msgpack_int(struct uwsgi_buffer *ub, int64_t num) {
	if (num > 0 && num <= 127) {
		return uwsgi_buffer_byte(ub, (uint8_t) num);
	}
	if (uwsgi_buffer_byte(ub, 0xD3)) return -1;
	return uwsgi_buffer_u32be(ub, num);
}

int uwsgi_buffer_msgpack_nil(struct uwsgi_buffer *ub) {
	return uwsgi_buffer_byte(ub, 0xC0);
}

int uwsgi_buffer_msgpack_true(struct uwsgi_buffer *ub) {
	return uwsgi_buffer_byte(ub, 0xC3);
}

int uwsgi_buffer_msgpack_false(struct uwsgi_buffer *ub) {
	return uwsgi_buffer_byte(ub, 0xC2);
}

static char *uwsgi_msgpack_log_encoder(struct uwsgi_log_encoder *ule, char *msg, size_t len, size_t *rlen) {
	char *buf = NULL;
	if (!ule->configured) {
		char *p = strtok(ule->args, "|");
		while(p) {
			char *colon = strchr(p, ':');
			if (colon) *colon = 0;	
			// find the type of item
			if (!strcmp(p, "map")) {
				struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_MAP);
				if (colon) {
					new_umi->num = strtol(colon+1, NULL, 10);
				}
			}
			else if (!strcmp(p, "array")) {
                                struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_ARRAY);
                                if (colon) {
                                        new_umi->num = strtol(colon+1, NULL, 10);
                                }
                        }
			else if (!strcmp(p, "nil")) {
				uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_NIL);
			}
			else if (!strcmp(p, "true")) {
				uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_TRUE);
			}
			else if (!strcmp(p, "false")) {
				uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_FALSE);
			}
			else if (!strcmp(p, "str")) {
                                struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_STR);
                                if (colon) {
                                        new_umi->str = colon+1;
					new_umi->str_len = strlen(colon+1);
				}
				else {
					new_umi->str = "";
				}
                        }
			else if (!strcmp(p, "bin")) {
                                struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_BIN);
                                if (colon) {
                                        new_umi->str = colon+1;
                                        new_umi->str_len = strlen(colon+1);
                                }
				else {
					new_umi->str = "";
				}
                        }
			else if (!strcmp(p, "msg")) {
				uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_MAGIC);
			}
			else if (!strcmp(p, "msgbin")) {
				struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_MAGIC);
				new_umi->num = 1;
			}

			else if (!strcmp(p, "msgnl")) {
                                struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_MAGIC);
                                new_umi->num = 2;
                        }
                        else if (!strcmp(p, "msgbinnl")) {
                                struct uwsgi_msgpack_item *new_umi = uwsgi_msgpack_item_add((struct uwsgi_msgpack_item**)&ule->data, MSGPACK_MAGIC);
                                new_umi->num = 3;
                        }
		
			if (colon) *colon = ':';
			p = strtok(NULL, "|");
		}
		ule->configured = 1;
	}

	struct uwsgi_buffer *ub = uwsgi_buffer_new(len + strlen(ule->args));
	struct uwsgi_msgpack_item *umi = (struct uwsgi_msgpack_item*) ule->data;
	uint32_t tmp_len = 0;
	while(umi) {
		switch(umi->type) {
			case(MSGPACK_NIL):
				if (uwsgi_buffer_msgpack_nil(ub)) goto end;
				break;
			case(MSGPACK_TRUE):
				if (uwsgi_buffer_msgpack_true(ub)) goto end;
				break;
			case(MSGPACK_FALSE):
				if (uwsgi_buffer_msgpack_false(ub)) goto end;
				break;
			case(MSGPACK_STR):
				if (uwsgi_buffer_msgpack_str(ub, umi->str, umi->str_len)) goto end;
				break;
			case(MSGPACK_BIN):
				if (uwsgi_buffer_msgpack_bin(ub, umi->str, umi->str_len)) goto end;
				break;
			case(MSGPACK_MAP):
				if (uwsgi_buffer_msgpack_map(ub, umi->num)) goto end;
				break;
			case(MSGPACK_ARRAY):
				if (uwsgi_buffer_msgpack_array(ub, umi->num)) goto end;
				break;
			case(MSGPACK_MAGIC):
				// msg
				tmp_len = len;
				if (umi->num == 0) {
					if (msg[len-1] == '\n') tmp_len--;
					if (uwsgi_buffer_msgpack_str(ub, msg, tmp_len)) goto end;
				}
				// msgbin
				else if (umi->num == 1) {
					if (msg[len-1] == '\n') tmp_len--;
					if (uwsgi_buffer_msgpack_bin(ub, msg, tmp_len)) goto end;
				}
				// msgnl
				if (umi->num == 2) {
                                        if (uwsgi_buffer_msgpack_str(ub, msg, tmp_len)) goto end;
                                }
                                // msgbinnl
                                else if (umi->num == 3) {
                                        if (uwsgi_buffer_msgpack_bin(ub, msg, tmp_len)) goto end;
                                }
				break;
			default:
				break;
		}
		umi = umi->next;
	}

	buf = ub->buf;
	*rlen = ub->pos;
	ub->buf = NULL;
end:
	uwsgi_buffer_destroy(ub);
	return buf;
}

static void uwsgi_msgpack_register() {
	uwsgi_register_log_encoder("msgpack", uwsgi_msgpack_log_encoder);
	uwsgi_register_log_encoder("msgpack5", uwsgi_msgpack_log_encoder);
}

struct uwsgi_plugin msgpack_plugin = {
	.name = "msgpack",
	.on_load = uwsgi_msgpack_register,
};