#!/home/edd1e/scripts/projs/uts_bot/uts_bot_env/bin/python3

import configuration as conf
import getpass
from time import sleep
import os
import logging

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.common.exceptions import NoSuchElementException
from dotenv import load_dotenv
load_dotenv()

logging.basicConfig(
    level=logging.INFO, 
    format='%(asctime)s [%(levelname)s|%(name)s|%(funcName)s]:: %(message)s', 
    handlers=[logging.StreamHandler()]
)
class Browser:

    browser, service = None, None
    def __init__(self, driver:str):
        logging.info("INITIALIZING")
        web_options = webdriver.ChromeOptions() 
        web_options.add_argument("--remote-debugging-port=9222")  # thisself.service = Service(driver)
        web_options.add_experimental_option("detach", True)
        self.browser = webdriver.Chrome(service=self.service,options=web_options)
        self.browser.set_page_load_timeout(10)
        

    def open_page(self,url:str):
        logging.info(f"OPENING:: {url}")
        self.browser.get(url)

    def click_button(self, element:object):
        element_to_string = str(element.text)
        if element_to_string == "":
            element_to_string = "UNKNOWN"
        logging.info(f"CLICKING ON ELEMENT:: {element_to_string}")
        element.click()


    def find_inner_element(self,by_param:str, element:str, is_single_element=True):
        if is_single_element:
            return self.browser.find_element(by_param, element)
        else:
            return self.browser.find_elements(by_param, element)



    def type_data(self, email_form:str, passwd_form:str, email:str, passwd:str):
        try:
            logging.info(f"TYPING DATA ON:: {email_form, passwd_form}")
            self.browser.find_element(By.NAME, email_form).send_keys(email + Keys.ENTER)
            sleep(1)
            self.browser.find_element(By.NAME, passwd_form).send_keys(passwd + Keys.ENTER)
        except NoSuchElementException:
            logging.warning("NoSuchElementException, trying again...")
            self.type_data(email_form, passwd_form, email, passwd)

    def go_back(self):
        logging.info("GOING TO PREVIOUS PAGE")
        self.browser.back()


    def close_browser(self):
        logging.info("CLOSING BROWSER")
        self.browser.close()



if __name__ == "__main__":

    browser = Browser(conf.CHROME_DRIVER_DIR)
    browser.open_page(conf.saia_page)
    browser.click_button(browser.find_inner_element(By.CLASS_NAME, "login-identityprovider-btn"))
    browser.type_data("loginfmt", "passwd")
    browser.click_button(browser.find_inner_element(By.ID,"idBtn_Back"))
    browser.click_button(browser.find_inner_element(By.CLASS_NAME,'primary-navigation').find_elements(By.TAG_NAME, "li")[2].find_element(By.TAG_NAME, 'a'))
    sleep(2)
    courses = browser.find_inner_element(By.CLASS_NAME,"dashboard-card-deck").find_elements(By.CLASS_NAME,"dashboard-card")

    for element in courses:
        browser.click_button(element.find_element(By.TAG_NAME, "a"))
        sleep(2)
        browser.go_back()
    
    # browser.click_button(By.LINK_TEXT, "https://saia2.uts.edu.ve/my/courses.php")
    # browser.close_browser()